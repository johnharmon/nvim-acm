package providers

import (
	"fmt"
	"strings"

	"github.com/autoshift/lsp-server/internal/catalog"
	"github.com/autoshift/lsp-server/internal/context"
	"github.com/autoshift/lsp-server/internal/values"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CompletionInput is everything a completion call needs from the server side.
type CompletionInput struct {
	URI         string
	FilePath    string
	Text        string
	Position    protocol.Position
	Catalog     catalog.Resolved
	ValuesCache *values.Cache
}

// Provide returns completion items for the given position.
func Provide(in CompletionInput) []protocol.CompletionItem {
	offset := offsetAt(in.Text, in.Position)
	ctx := context.DetectLayerAt(in.Text, offset)
	if ctx.Layer == context.LayerNone {
		return nil
	}

	if ctx.Layer == context.LayerHelm {
		if items, ok := tryValuesCompletion(in, offset); ok {
			return items
		}
	}

	var funcs []catalog.TemplateFunction
	var vars []catalog.ExportedValue
	switch ctx.Layer {
	case context.LayerHelm:
		funcs = append(funcs, in.Catalog.HelmFunctions...)
		funcs = append(funcs, in.Catalog.GoBuiltins...)
		vars = append(vars, in.Catalog.HelmContextValues...)
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

	items := make([]protocol.CompletionItem, 0, len(funcs)+len(vars))
	for _, fn := range funcs {
		items = append(items, funcItem(fn))
	}
	for _, v := range vars {
		items = append(items, valueItem(v))
	}
	return items
}

func tryValuesCompletion(in CompletionInput, offset int) ([]protocol.CompletionItem, bool) {
	parsed, ok := values.ParseValuesPathBeforeCursor(in.Text, offset)
	if !ok {
		return nil, false
	}
	if in.ValuesCache == nil || in.FilePath == "" {
		return nil, true
	}
	chartRoot := values.FindChartRoot(in.FilePath)
	if chartRoot == "" {
		return nil, true
	}
	root := in.ValuesCache.Get(chartRoot)
	if root == nil {
		return nil, true
	}
	parent := values.Navigate(root, parsed.Segments)
	if parent == nil || parent.Type != values.TypeMap || parent.Children == nil {
		return []protocol.CompletionItem{}, true
	}
	items := make([]protocol.CompletionItem, 0, len(parent.Children))
	for key, child := range parent.Children {
		items = append(items, valuesChildItem(key, child, append(parsed.Segments, key)))
	}
	return items, true
}

func funcItem(fn catalog.TemplateFunction) protocol.CompletionItem {
	kind := protocol.CompletionItemKindFunction
	doc := buildFuncMarkdown(fn)
	detail := fn.Signature
	insert := fn.Name
	insertFmt := protocol.InsertTextFormatPlainText
	if len(fn.Params) > 0 {
		insertFmt = protocol.InsertTextFormatSnippet
		args := make([]string, len(fn.Params))
		for i, p := range fn.Params {
			args[i] = fmt.Sprintf(`${%d:%s}`, i+1, p.Name)
		}
		insert = fn.Name + " " + strings.Join(args, " ")
	}
	return protocol.CompletionItem{
		Label:            fn.Name,
		Kind:             &kind,
		Detail:           &detail,
		Documentation:    doc,
		InsertText:       &insert,
		InsertTextFormat: &insertFmt,
		SortText:         strPtr("1-" + fn.Name),
	}
}

func valueItem(v catalog.ExportedValue) protocol.CompletionItem {
	kind := protocol.CompletionItemKindVariable
	detail := v.Type
	doc := protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: v.Description}
	// Catalog stores names with a leading dot for display (".ManagedClusterName"),
	// but most LSP clients filter against the word-at-cursor, which excludes `.`
	// from yaml/helm word characters. Without filterText/insertText overrides
	// the client never matches when the user types `.M` then expects a hit.
	bare := strings.TrimPrefix(v.Name, ".")
	return protocol.CompletionItem{
		Label:         v.Name,
		Kind:          &kind,
		Detail:        &detail,
		Documentation: doc,
		SortText:      strPtr("0-" + bare),
		FilterText:    &bare,
		InsertText:    &bare,
	}
}

func valuesChildItem(key string, child *values.Node, fullPath []string) protocol.CompletionItem {
	kind := childKind(child.Type)
	detail := renderValuesDetail(child)
	doc := renderValuesDoc(child, fullPath)
	return protocol.CompletionItem{
		Label:         key,
		Kind:          &kind,
		Detail:        &detail,
		Documentation: doc,
		SortText:      strPtr("0-" + key),
	}
}

func buildFuncMarkdown(fn catalog.TemplateFunction) protocol.MarkupContent {
	var b strings.Builder
	b.WriteString("```go\n")
	b.WriteString(fn.Signature)
	b.WriteString("\n```\n\n")
	b.WriteString(fn.Description)
	if len(fn.Params) > 0 {
		b.WriteString("\n\n**Parameters:**")
		for _, p := range fn.Params {
			variadic := ""
			if p.Variadic {
				variadic = "..."
			}
			optional := ""
			if p.Optional {
				optional = "?"
			}
			fmt.Fprintf(&b, "\n- `%s%s%s: %s`", variadic, p.Name, optional, p.Type)
			if p.Description != "" {
				fmt.Fprintf(&b, " — %s", p.Description)
			}
		}
	}
	fmt.Fprintf(&b, "\n\n**Returns:** `%s`", fn.Returns.Type)
	if fn.Returns.Description != "" {
		fmt.Fprintf(&b, " — %s", fn.Returns.Description)
	}
	if len(fn.Examples) > 0 {
		b.WriteString("\n\n**Examples:**")
		for _, ex := range fn.Examples {
			b.WriteString("\n```yaml\n")
			b.WriteString(ex)
			b.WriteString("\n```")
		}
	}
	if fn.Since != "" {
		fmt.Fprintf(&b, "\n\n_Since ACM %s._", fn.Since)
	}
	if fn.Deprecated != "" {
		fmt.Fprintf(&b, "\n\n⚠ Deprecated: %s", fn.Deprecated)
	}
	if fn.Source != "" {
		fmt.Fprintf(&b, "\n\n_Source: %s._", fn.Source)
	}
	return protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: b.String()}
}

func renderValuesDetail(node *values.Node) string {
	switch node.Type {
	case values.TypeMap:
		return "map"
	case values.TypeList:
		if node.Example != "" {
			return node.Example
		}
		return "list"
	default:
		if node.Example != "" {
			return fmt.Sprintf("%s = %s", node.Type, truncate(node.Example, 60))
		}
		return string(node.Type)
	}
}

func renderValuesDoc(node *values.Node, fullPath []string) protocol.MarkupContent {
	var b strings.Builder
	fmt.Fprintf(&b, "```go\n.Values.%s: %s\n```\n", strings.Join(fullPath, "."), node.Type)
	if node.Description != "" {
		fmt.Fprintf(&b, "\n%s", node.Description)
	}
	if node.Example != "" && node.Type != values.TypeMap {
		fmt.Fprintf(&b, "\n\n**Default:** `%s`", truncate(node.Example, 200))
	}
	return protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: b.String()}
}

func childKind(t values.ValueType) protocol.CompletionItemKind {
	switch t {
	case values.TypeMap:
		return protocol.CompletionItemKindStruct
	case values.TypeList:
		return protocol.CompletionItemKindVariable
	case values.TypeString:
		return protocol.CompletionItemKindText
	case values.TypeNumber, values.TypeBoolean:
		return protocol.CompletionItemKindValue
	}
	return protocol.CompletionItemKindField
}

func strPtr(s string) *string { return &s }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// offsetAt converts an LSP Position to a byte offset assuming UTF-16 code-unit
// counting; for ASCII-mostly YAML/Helm this is equivalent to byte counting.
// Treesitter-grammar handling will revisit this if multibyte chars appear.
func offsetAt(text string, pos protocol.Position) int {
	line := uint32(0)
	for i := 0; i < len(text); i++ {
		if line == pos.Line {
			return i + int(pos.Character)
		}
		if text[i] == '\n' {
			line++
		}
	}
	return len(text)
}
