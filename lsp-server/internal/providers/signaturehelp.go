package providers

import (
	"fmt"
	"strings"

	"github.com/autoshift/lsp-server/internal/catalog"
	"github.com/autoshift/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// SignatureHelpInput packages everything SignatureHelp needs.
type SignatureHelpInput struct {
	Text     string
	Position protocol.Position
	Catalog  catalog.Resolved
}

// SignatureHelp returns parameter hints for the active call at the cursor.
func SignatureHelp(in SignatureHelpInput) *protocol.SignatureHelp {
	offset := offsetAt(in.Text, in.Position)
	ctx := context.DetectLayerAt(in.Text, offset)
	if ctx.Layer == context.LayerNone || ctx.MustacheStart == -1 {
		return nil
	}
	funcs := pickFuncs(in.Catalog, ctx.Layer)
	fn, argIdx := findActiveCall(in.Text, ctx.MustacheStart, offset, funcs)
	if fn == nil {
		return nil
	}
	sig := buildSignature(*fn)
	active := clampParamIndex(*fn, argIdx)
	sig.ActiveParameter = uint32Ptr(uint32(active))
	help := &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{sig},
		ActiveSignature: uint32Ptr(0),
		ActiveParameter: uint32Ptr(uint32(active)),
	}
	return help
}

func pickFuncs(c catalog.Resolved, layer context.Layer) []catalog.TemplateFunction {
	out := []catalog.TemplateFunction{}
	switch layer {
	case context.LayerHelm:
		out = append(out, c.HelmFunctions...)
		out = append(out, c.GoBuiltins...)
	case context.LayerHub:
		out = append(out, c.HubFunctions...)
		out = append(out, c.SprigFunctions...)
		out = append(out, c.GoBuiltins...)
	case context.LayerManaged:
		out = append(out, c.ManagedFunctions...)
		out = append(out, c.SprigFunctions...)
		out = append(out, c.GoBuiltins...)
	}
	return out
}

type tokKind int

const (
	tokIdent tokKind = iota
	tokString
	tokPunct
	tokWS
)

type tok struct {
	kind tokKind
	text string
}

func tokenize(s string) []tok {
	out := []tok{}
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				i++
			}
			out = append(out, tok{kind: tokWS})
			continue
		}
		if c == '"' || c == '`' {
			q := c
			j := i + 1
			for j < len(s) && s[j] != q {
				if s[j] == '\\' && q == '"' && j+1 < len(s) {
					j += 2
					continue
				}
				j++
			}
			end := j + 1
			if end > len(s) {
				end = len(s)
			}
			out = append(out, tok{kind: tokString, text: s[i:end]})
			i = end
			continue
		}
		if isIdentStart(c) || c == '.' || c == '$' {
			j := i + 1
			for j < len(s) && (isIdentCont(s[j]) || s[j] == '.' || s[j] == '$') {
				j++
			}
			out = append(out, tok{kind: tokIdent, text: s[i:j]})
			i = j
			continue
		}
		out = append(out, tok{kind: tokPunct, text: string(c)})
		i++
	}
	return out
}

func isIdentStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func findActiveCall(text string, mustacheStart, cursor int, funcs []catalog.TemplateFunction) (*catalog.TemplateFunction, int) {
	if mustacheStart+2 > cursor || cursor > len(text) {
		return nil, -1
	}
	inside := text[mustacheStart+2 : cursor]
	tokens := tokenize(inside)
	for i := len(tokens) - 1; i >= 0; i-- {
		if tokens[i].kind != tokIdent {
			continue
		}
		fn := findFuncByName(funcs, tokens[i].text)
		if fn == nil {
			continue
		}
		argIdx := countArgsSinceIdent(tokens[i+1:])
		return fn, argIdx
	}
	return nil, -1
}

func findFuncByName(funcs []catalog.TemplateFunction, name string) *catalog.TemplateFunction {
	for i := range funcs {
		if funcs[i].Name == name {
			return &funcs[i]
		}
	}
	return nil
}

func countArgsSinceIdent(after []tok) int {
	hasArg := false
	for _, t := range after {
		if t.kind == tokString || t.kind == tokIdent {
			hasArg = true
			break
		}
	}
	if !hasArg {
		for _, t := range after {
			if t.kind == tokWS {
				return 0
			}
		}
		return -1
	}
	args := 0
	sawArg := false
	for _, t := range after {
		switch t.kind {
		case tokWS:
			if sawArg {
				args++
				sawArg = false
			}
		case tokString, tokIdent:
			sawArg = true
		case tokPunct:
			if t.text == "|" || t.text == "(" || t.text == ")" {
				return -1
			}
		}
	}
	return args
}

func buildSignature(fn catalog.TemplateFunction) protocol.SignatureInformation {
	doc := protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: fn.Description}
	if fn.Source != "" {
		doc.Value = fn.Description + "\n\n_Source: " + fn.Source + "._"
	}
	params := make([]protocol.ParameterInformation, len(fn.Params))
	for i, p := range fn.Params {
		variadic := ""
		if p.Variadic {
			variadic = "..."
		}
		optional := ""
		if p.Optional {
			optional = "?"
		}
		label := fmt.Sprintf("%s%s%s: %s", variadic, p.Name, optional, p.Type)
		pdoc := protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: p.Description}
		params[i] = protocol.ParameterInformation{Label: label, Documentation: pdoc}
	}
	return protocol.SignatureInformation{
		Label:         fn.Signature,
		Documentation: doc,
		Parameters:    params,
	}
}

func clampParamIndex(fn catalog.TemplateFunction, idx int) int {
	if idx < 0 {
		return 0
	}
	if len(fn.Params) == 0 {
		return 0
	}
	max := len(fn.Params) - 1
	last := fn.Params[max]
	if last.Variadic && idx > max {
		return max
	}
	if idx > max {
		return max
	}
	return idx
}

func uint32Ptr(v uint32) *uint32 { return &v }

// strings is referenced indirectly via fmt.Sprintf — keep import alive.
var _ = strings.Builder{}
