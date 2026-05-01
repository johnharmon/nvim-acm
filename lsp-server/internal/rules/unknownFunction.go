package rules

import (
	"fmt"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CatalogResolver is the subset of catalog.Loader the rule needs. Defining it
// as an interface keeps the rule unit-testable without touching disk.
type CatalogResolver interface {
	Resolve(version string, extras catalog.UserExtras) catalog.Resolved
}

type unknownFunction struct {
	resolver CatalogResolver
}

// NewUnknownFunction constructs the rule with access to a catalog resolver so
// it can resolve the configured ACM version against on-disk function lists at
// rule-evaluation time. Default behavior is opt-in: the catalog's sprig
// coverage is intentionally a subset, so false positives are non-zero.
func NewUnknownFunction(resolver CatalogResolver) Rule {
	return unknownFunction{resolver: resolver}
}

func (unknownFunction) ID() string { return "unknown-function" }

func (r unknownFunction) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.unknown-function.enabled", false) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.unknown-function.severity", string(SeverityWarning)))
	version := Get(ctx.Settings, "acm.version", "2.15")
	resolved := r.resolver.Resolve(version, catalog.UserExtras{})

	known := buildKnownFunctionSet(resolved)
	for _, n := range GetStringSlice(ctx.Settings, "rules.unknown-function.allowedFunctions", nil) {
		known[n] = true
	}

	out := []protocol.Diagnostic{}
	seen := map[int]bool{}
	sev := severity.ToLSP()
	code := protocol.IntegerOrString{Value: "unknown-function"}
	source := "acm"

	emit := func(content string, base int) {
		for _, ir := range findFunctionCallIdents(content) {
			absStart := base + ir.start
			absEnd := base + ir.end
			if seen[absStart] {
				continue
			}
			seen[absStart] = true
			name := content[ir.start:ir.end]
			if known[name] {
				continue
			}
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: offsetToPosition(ctx.Text, absStart),
					End:   offsetToPosition(ctx.Text, absEnd),
				},
				Severity: &sev,
				Code:     &code,
				Source:   &source,
				Message:  fmt.Sprintf(`Unknown function %q. Not in helm/hub/managed/sprig/Go-builtins for ACM %s. Add to rules.unknown-function.allowedFunctions to silence.`, name, resolved.AcmVersion),
			})
		}
	}

	for _, sp := range context.FindHubSpans(ctx.Text) {
		emit(ctx.Text[sp.ContentStart:sp.ContentEnd], sp.ContentStart)
	}
	for _, sp := range context.FindManagedSpans(ctx.Text) {
		emit(ctx.Text[sp.ContentStart:sp.ContentEnd], sp.ContentStart)
	}
	for _, sp := range findExpressionInners(ctx.Text) {
		emit(ctx.Text[sp.start:sp.end], sp.start)
	}

	return out
}

func buildKnownFunctionSet(c catalog.Resolved) map[string]bool {
	out := map[string]bool{}
	add := func(fns []catalog.TemplateFunction) {
		for _, f := range fns {
			out[f.Name] = true
		}
	}
	add(c.HelmFunctions)
	add(c.HubFunctions)
	add(c.ManagedFunctions)
	add(c.SprigFunctions)
	add(c.GoBuiltins)
	return out
}

// identRange marks an identifier byte range within the inner string passed to
// findFunctionCallIdents.
type identRange struct {
	start, end int
}

// findFunctionCallIdents scans the inner content of a template span and
// returns ranges for every bare identifier that's syntactically a function
// call. It skips control/operator/literal keywords, `.foo` property paths,
// `$var` references, and string-literal contents.
func findFunctionCallIdents(s string) []identRange {
	out := []identRange{}
	n := len(s)
	i := 0
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
			c == '(' || c == ')' || c == '[' || c == ']' ||
			c == '|' || c == ',' || c == ':' || c == ';':
			i++
		case c == '"':
			i++
			for i < n && s[i] != '"' {
				if s[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i < n {
				i++
			}
		case c == '`':
			i++
			for i < n && s[i] != '`' {
				i++
			}
			if i < n {
				i++
			}
		case c == '$':
			i++
			for i < n && isWordByte(s[i]) {
				i++
			}
		case c == '.':
			i++
			for i < n && isWordByte(s[i]) {
				i++
			}
		case isDigitByte(c):
			for i < n && (isDigitByte(s[i]) || s[i] == '.') {
				i++
			}
		case isIdentStart(c):
			start := i
			for i < n && isWordByte(s[i]) {
				i++
			}
			name := s[start:i]
			if !templateReservedWord[name] {
				out = append(out, identRange{start: start, end: i})
			}
		default:
			i++
		}
	}
	return out
}

// templateReservedWord covers control flow, comparison operators, literals,
// and the `hub` marker — none of these are function calls even though they
// look like bare identifiers in `{{ ... }}`.
var templateReservedWord = map[string]bool{
	"if": true, "else": true, "end": true, "range": true, "with": true,
	"template": true, "define": true, "block": true, "return": true,
	"break": true, "continue": true, "hub": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"and": true, "or": true, "not": true,
	"true": true, "false": true, "nil": true,
}

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}
func isWordByte(c byte) bool { return isIdentStart(c) || isDigitByte(c) }

// exprSpanInner is the byte range between `{{`/`{{-` and `}}`/`-}}` (trim
// markers excluded). Used by rules that want to walk every helm-context
// expression body in document order.
type exprSpanInner struct {
	start, end int
}

func findExpressionInners(text string) []exprSpanInner {
	out := []exprSpanInner{}
	n := len(text)
	i := 0
	for i+1 < n {
		if text[i] != '{' || text[i+1] != '{' {
			i++
			continue
		}
		innerStart := i + 2
		if innerStart < n && text[innerStart] == '-' {
			innerStart++
		}
		end := findExprClose(text, innerStart)
		if end < 0 {
			break
		}
		innerEnd := end
		if innerEnd > innerStart && text[innerEnd-1] == '-' {
			innerEnd--
		}
		out = append(out, exprSpanInner{start: innerStart, end: innerEnd})
		i = end + 2
	}
	return out
}

// findExprClose returns the offset of the first `}` of the closing `}}`
// starting from `from`, accounting for string literals (`"..."` and
// `\`...\``). Returns -1 if there is no closing `}}` before EOF.
func findExprClose(text string, from int) int {
	n := len(text)
	j := from
	for j+1 < n {
		c := text[j]
		switch c {
		case '"':
			j++
			for j < n && text[j] != '"' {
				if text[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				j++
			}
			if j < n {
				j++
			}
		case '`':
			j++
			for j < n && text[j] != '`' {
				j++
			}
			if j < n {
				j++
			}
		case '/':
			// `/* … */` go-template comment — skip the entire body so
			// `}}` inside the comment doesn't falsely close the
			// surrounding expression.
			if j+1 < n && text[j+1] == '*' {
				j += 2
				for j+1 < n && !(text[j] == '*' && text[j+1] == '/') {
					j++
				}
				if j+1 < n {
					j += 2
				} else {
					return -1
				}
				continue
			}
			j++
		case '}':
			if j+1 < n && text[j+1] == '}' {
				return j
			}
			j++
		default:
			j++
		}
	}
	return -1
}
