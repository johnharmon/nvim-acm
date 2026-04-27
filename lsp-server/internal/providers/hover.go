package providers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autoshift/lsp-server/internal/catalog"
	"github.com/autoshift/lsp-server/internal/context"
	"github.com/autoshift/lsp-server/internal/values"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// HoverInput packages everything Hover needs.
type HoverInput struct {
	URI         string
	FilePath    string
	Text        string
	Position    protocol.Position
	Catalog     catalog.Resolved
	ValuesCache *values.Cache
}

var (
	wordRE       = regexp.MustCompile(`[\w.$]+`)
	valuesRefRE  = regexp.MustCompile(`\.?\$?\.?Values(?:\.[A-Za-z0-9_]+)+`)
)

// Hover returns the hover info for the position, or nil if nothing matches.
func Hover(in HoverInput) *protocol.Hover {
	offset := offsetAt(in.Text, in.Position)
	ctx := context.DetectLayerAt(in.Text, offset)
	if ctx.Layer == context.LayerNone {
		return nil
	}
	if ctx.Layer == context.LayerHelm {
		if h := tryValuesHover(in, offset); h != nil {
			return h
		}
	}

	wordRange := wordRangeAt(in.Text, offset, wordRE)
	if wordRange == nil {
		return nil
	}
	word := in.Text[wordRange.startOffset:wordRange.endOffset]

	var funcs []catalog.TemplateFunction
	var vars []catalog.ExportedValue
	switch ctx.Layer {
	case context.LayerHelm:
		funcs = append(funcs, in.Catalog.HelmFunctions...)
		funcs = append(funcs, in.Catalog.GoBuiltins...)
	case context.LayerHub:
		funcs = append(funcs, in.Catalog.HubFunctions...)
		funcs = append(funcs, in.Catalog.SprigFunctions...)
		funcs = append(funcs, in.Catalog.GoBuiltins...)
		vars = in.Catalog.HubExportedValues
	case context.LayerManaged:
		funcs = append(funcs, in.Catalog.ManagedFunctions...)
		funcs = append(funcs, in.Catalog.SprigFunctions...)
		funcs = append(funcs, in.Catalog.GoBuiltins...)
		vars = in.Catalog.ManagedExportedValues
	}

	if fn := findFunc(funcs, word); fn != nil {
		md := buildFuncMarkdown(*fn)
		rng := wordRange.toLSP(in.Text)
		return &protocol.Hover{
			Contents: md,
			Range:    &rng,
		}
	}
	if v := findValue(vars, word); v != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "```go\n%s: %s\n```\n\n%s", v.Name, v.Type, v.Description)
		if v.Source != "" {
			fmt.Fprintf(&b, "\n\n_Source: %s._", v.Source)
		}
		md := protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: b.String()}
		rng := wordRange.toLSP(in.Text)
		return &protocol.Hover{
			Contents: md,
			Range:    &rng,
		}
	}
	return nil
}

func tryValuesHover(in HoverInput, offset int) *protocol.Hover {
	rng := matchAroundOffset(in.Text, offset, valuesRefRE)
	if rng == nil {
		return nil
	}
	raw := in.Text[rng.startOffset:rng.endOffset]
	cleaned := raw
	if strings.HasPrefix(cleaned, "$.") {
		cleaned = cleaned[2:]
	} else if strings.HasPrefix(cleaned, ".") {
		cleaned = cleaned[1:]
	}
	if !strings.HasPrefix(cleaned, "Values.") {
		return nil
	}
	cleaned = cleaned[len("Values."):]
	segments := []string{}
	for _, s := range strings.Split(cleaned, ".") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) == 0 || in.ValuesCache == nil || in.FilePath == "" {
		return nil
	}
	chartRoot := values.FindChartRoot(in.FilePath)
	if chartRoot == "" {
		return nil
	}
	root := in.ValuesCache.Get(chartRoot)
	if root == nil {
		return nil
	}
	node := values.Navigate(root, segments)
	if node == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "```go\n.Values.%s: %s\n```", strings.Join(segments, "."), node.Type)
	if node.Description != "" {
		fmt.Fprintf(&b, "\n\n%s", node.Description)
	}
	if node.Example != "" && node.Type != values.TypeMap {
		fmt.Fprintf(&b, "\n\n**Default:** `%s`", truncate(node.Example, 200))
	}
	if node.Type == values.TypeMap && node.Children != nil {
		keys := []string{}
		for k := range node.Children {
			keys = append(keys, k)
		}
		fmt.Fprintf(&b, "\n\n**Keys:** %s", strings.Join(keys, ", "))
	}
	md := protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: b.String()}
	lspRange := rng.toLSP(in.Text)
	return &protocol.Hover{
		Contents: md,
		Range:    &lspRange,
	}
}

type byteRange struct {
	startOffset int
	endOffset   int
}

func (br byteRange) toLSP(text string) protocol.Range {
	return protocol.Range{
		Start: positionAt(text, br.startOffset),
		End:   positionAt(text, br.endOffset),
	}
}

func wordRangeAt(text string, offset int, pattern *regexp.Regexp) *byteRange {
	if offset > len(text) {
		offset = len(text)
	}
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	lineEnd := offset + strings.IndexByte(text[offset:], '\n')
	if lineEnd < offset {
		lineEnd = len(text)
	}
	line := text[lineStart:lineEnd]
	relCursor := offset - lineStart
	for _, m := range pattern.FindAllStringIndex(line, -1) {
		if m[0] <= relCursor && relCursor <= m[1] {
			return &byteRange{startOffset: lineStart + m[0], endOffset: lineStart + m[1]}
		}
	}
	return nil
}

func matchAroundOffset(text string, offset int, pattern *regexp.Regexp) *byteRange {
	return wordRangeAt(text, offset, pattern)
}

func findFunc(funcs []catalog.TemplateFunction, name string) *catalog.TemplateFunction {
	for i := range funcs {
		if funcs[i].Name == name {
			return &funcs[i]
		}
	}
	return nil
}

func findValue(vars []catalog.ExportedValue, name string) *catalog.ExportedValue {
	for i := range vars {
		if vars[i].Name == name || vars[i].Name == "."+name {
			return &vars[i]
		}
	}
	return nil
}

func positionAt(text string, offset int) protocol.Position {
	if offset > len(text) {
		offset = len(text)
	}
	line := uint32(0)
	col := uint32(0)
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return protocol.Position{Line: line, Character: col}
}
