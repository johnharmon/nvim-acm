package rules

import (
	"regexp"
	"text/template/parse"

	"github.com/acm-ls/lsp-server/internal/catalog"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type templateSyntax struct {
	resolver CatalogResolver
}

// NewTemplateSyntax constructs the rule with access to the catalog resolver.
// At rule-evaluation time the resolver is queried for every known
// helm/hub/managed/sprig/Go-builtin function name; those are registered as
// no-op stubs in the parser's FuncMap so that legitimate ACM calls aren't
// mis-classified as undefined identifiers. `hub` is also stubbed so that
// the direct-form hub expression `{{hub fn args hub}}` parses cleanly as
// an action whose first identifier is the `hub` "function".
func NewTemplateSyntax(resolver CatalogResolver) Rule {
	return templateSyntax{resolver: resolver}
}

func (templateSyntax) ID() string { return "template-syntax" }

func (r templateSyntax) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.template-syntax.enabled", true) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.template-syntax.severity", string(SeverityWarning)))
	version := Get(ctx.Settings, "acm.version", "2.15")
	resolved := r.resolver.Resolve(version, catalog.UserExtras{})

	funcs := buildStubFuncMap(resolved)

	sev := severity.ToLSP()
	code := protocol.IntegerOrString{Value: "template-syntax"}
	source := "acm"

	out := []protocol.Diagnostic{}
	for _, span := range findObjectTemplatesRawBlocks(ctx.Text) {
		body := ctx.Text[span.contentStart:span.contentEnd]
		if _, err := parse.Parse("ot-raw", body, "{{", "}}", funcs); err != nil {
			perr, ok := parseTemplateError(err.Error())
			if !ok {
				// Couldn't parse the position out of the error — surface it
				// at the top of the block scalar instead of dropping it.
				out = append(out, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(span.contentLine), Character: 0},
						End:   protocol.Position{Line: uint32(span.contentLine), Character: 1},
					},
					Severity: &sev, Code: &code, Source: &source,
					Message: "template parse error: " + err.Error(),
				})
				continue
			}
			absLine := span.contentLine + (perr.line - 1)
			// Errors like "unexpected EOF" report a line one past the last
			// body line. Clamp into the block so the diagnostic lands on a
			// real content line where the user can see it.
			lastLine := span.contentLine
			if span.contentEnd > span.contentStart {
				lastLine = lineOfOffset(ctx.Text, span.contentEnd-1)
			}
			if absLine > lastLine {
				absLine = lastLine
			}
			lineLen := lineLengthAt(ctx.Text, absLine)
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: protocol.Position{Line: uint32(absLine), Character: 0},
					End:   protocol.Position{Line: uint32(absLine), Character: uint32(lineLen)},
				},
				Severity: &sev, Code: &code, Source: &source,
				Message: "template parse error: " + perr.msg,
			})
		}
	}
	return out
}

func buildStubFuncMap(c catalog.Resolved) map[string]any {
	stub := func(args ...any) any { return nil }
	out := map[string]any{
		// `hub` itself is not a real function but it appears in the action
		// position of direct hub forms (`{{hub fn args hub}}`) and as the
		// trailing identifier; registering it keeps the parser happy.
		"hub": stub,
	}
	add := func(fns []catalog.TemplateFunction) {
		for _, f := range fns {
			out[f.Name] = stub
		}
	}
	add(c.HelmFunctions)
	add(c.HubFunctions)
	add(c.ManagedFunctions)
	add(c.SprigFunctions)
	add(c.GoBuiltins)
	return out
}

// blockScalarSpan is the byte range of a YAML literal block-scalar's
// content (the body after the `|` indicator), recovered from the raw
// document text with original indentation preserved so parser error
// line numbers map directly back to document lines.
type blockScalarSpan struct {
	contentStart int // absolute byte offset of first content line
	contentEnd   int // absolute byte offset just past the last content byte
	contentLine  int // 0-indexed document line of the first content line
}

// blockKeyRE matches an `object-templates-raw:` line whose value is a
// literal block-scalar (`|`, `|+`, `|-`, `|<digit>`, etc.). Folded
// (`>`) variants don't appear in real ACM policies and would produce
// different effective content (paragraph-folded), so we skip them.
var blockKeyRE = regexp.MustCompile(`(?m)^(\s*)object-templates-raw:\s*\|[+-]?\d*\s*$`)

func findObjectTemplatesRawBlocks(text string) []blockScalarSpan {
	lines := splitDocLines(text)
	out := []blockScalarSpan{}
	for i, ln := range lines {
		m := blockKeyRE.FindStringSubmatch(ln.text)
		if m == nil {
			continue
		}
		keyIndent := len(m[1])
		if i+1 >= len(lines) {
			continue
		}
		contentLine := i + 1
		contentStart := lines[contentLine].offset
		contentEnd := contentStart
		j := contentLine
		for j < len(lines) {
			lt := lines[j]
			if isBlankLine(lt.text) {
				contentEnd = lt.offset + lt.totalLen
				j++
				continue
			}
			indent := leadingSpaces(lt.text)
			if indent <= keyIndent {
				break
			}
			contentEnd = lt.offset + lt.totalLen
			j++
		}
		if contentEnd > contentStart {
			out = append(out, blockScalarSpan{
				contentStart: contentStart,
				contentEnd:   contentEnd,
				contentLine:  contentLine,
			})
		}
	}
	return out
}

type docLine struct {
	offset   int    // byte offset of the line's first character
	text     string // line content excluding any trailing line terminator
	totalLen int    // bytes including the trailing `\n` (or `\r\n`) if present
}

func splitDocLines(text string) []docLine {
	out := []docLine{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			content := text[start:i]
			// Strip a trailing `\r` (CRLF support).
			if len(content) > 0 && content[len(content)-1] == '\r' {
				content = content[:len(content)-1]
			}
			out = append(out, docLine{offset: start, text: content, totalLen: i - start + 1})
			start = i + 1
		}
	}
	if start < len(text) {
		content := text[start:]
		if len(content) > 0 && content[len(content)-1] == '\r' {
			content = content[:len(content)-1]
		}
		out = append(out, docLine{offset: start, text: content, totalLen: len(text) - start})
	}
	return out
}

func leadingSpaces(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return len(s)
}

func isBlankLine(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}

func lineOfOffset(text string, offset int) int {
	if offset > len(text) {
		offset = len(text)
	}
	line := 0
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
		}
	}
	return line
}

func lineLengthAt(text string, line int) int {
	cur := 0
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			if cur == line {
				end := i
				if end > start && text[end-1] == '\r' {
					end--
				}
				return end - start
			}
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return len(text) - start
	}
	return 0
}

// templateParseError parses the standardized error format
// `template: NAME:LINE: MSG` (or, on newer Go, `template: NAME:LINE:COL: MSG`).
type templateParseError struct {
	line int
	msg  string
}

var parseErrRE = regexp.MustCompile(`^template:\s*\S+?:(\d+)(?::\d+)?:\s*(.+)$`)

func parseTemplateError(s string) (templateParseError, bool) {
	m := parseErrRE.FindStringSubmatch(s)
	if m == nil {
		return templateParseError{}, false
	}
	line := 0
	for i := 0; i < len(m[1]); i++ {
		line = line*10 + int(m[1][i]-'0')
	}
	if line < 1 {
		line = 1
	}
	return templateParseError{line: line, msg: m[2]}, true
}
