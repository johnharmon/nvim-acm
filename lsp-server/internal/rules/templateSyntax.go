package rules

import (
	"regexp"
	"strings"
	"text/template/parse"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/values"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type templateSyntax struct {
	resolver CatalogResolver
	cache    *values.Cache
}

// NewTemplateSyntax constructs the rule with access to the catalog resolver
// and the chart-values cache. At rule-evaluation time the resolver is
// queried for every known helm/hub/managed/sprig/Go-builtin function name;
// those are registered as no-op stubs in the parser's FuncMap so that
// legitimate ACM calls aren't mis-classified as undefined identifiers.
// `hub` is also stubbed so that the direct-form hub expression
// `{{hub fn args hub}}` parses cleanly as an action whose first identifier
// is the `hub` "function".
//
// The cache is consumed by the optional `executeHelm` Phase A.1 path: when
// `rules.template-syntax.executeHelm = true`, each block-scalar is also
// run through `text/template.Execute` against a data context built from
// the merged chart values + overlay tree. Default off (it's experimental
// and would change diagnostic surface area for users who haven't opted in).
func NewTemplateSyntax(resolver CatalogResolver, cache *values.Cache) Rule {
	return templateSyntax{resolver: resolver, cache: cache}
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
	layered := Get(ctx.Settings, "rules.template-syntax.layered", false)
	typedStubs := Get(ctx.Settings, "rules.template-syntax.typedStubs", false)
	hubFuncs := map[string]any(buildHubStubFuncs(resolved))
	managedFuncs := map[string]any(buildManagedStubFuncs(resolved))

	var valuesRoot *values.Node
	if layered && r.cache != nil && ctx.FilePath != "" {
		if root := values.FindChartRoot(ctx.FilePath); root != "" {
			valuesRoot = r.cache.Get(root)
		}
	}

	sev := severity.ToLSP()
	code := protocol.IntegerOrString{Value: "template-syntax"}
	source := "acm"

	var diagnostics []protocol.Diagnostic
	// emit produces a diagnostic for a parse error reported at line N
	// of `renderedSrc`. When `renderedSrc` is empty (stage 1, no render
	// step), N is treated as a body line directly. Otherwise the error
	// line is read from the rendered output and matched back to the
	// original block-scalar body via substring search — accurate when
	// helm/hub passes leave the line text mostly intact (the common
	// case for escape patterns), with a line-arithmetic fallback when
	// no match is found.
	emit := func(span blockScalarSpan, msgPrefix, fullErr, renderedSrc string) {
		perr, ok := parseTemplateError(fullErr)
		if !ok {
			diagnostics = append(diagnostics, protocol.Diagnostic{
				Range: protocol.Range{
					Start: protocol.Position{Line: uint32(span.contentLine), Character: 0},
					End:   protocol.Position{Line: uint32(span.contentLine), Character: 1},
				},
				Severity: &sev, Code: &code, Source: &source,
				Message: msgPrefix + ": " + fullErr,
			})
			return
		}
		body := ctx.Text[span.contentStart:span.contentEnd]
		absLine, contextLine := mapErrLineToDocument(span, body, renderedSrc, perr.line)
		// Clamp into block content for EOF-style errors that report a
		// line past the last body line.
		lastLine := span.contentLine
		if span.contentEnd > span.contentStart {
			lastLine = lineOfOffset(ctx.Text, span.contentEnd-1)
		}
		if absLine > lastLine {
			absLine = lastLine
		}
		lineLen := lineLengthAt(ctx.Text, absLine)
		message := msgPrefix + ": " + perr.msg
		if renderedSrc != "" && contextLine != "" {
			message += " | rendered: " + contextLine
		}
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: uint32(absLine), Character: 0},
				End:   protocol.Position{Line: uint32(absLine), Character: uint32(lineLen)},
			},
			Severity: &sev, Code: &code, Source: &source,
			Message: message,
		})
	}

	for _, span := range findObjectTemplatesRawBlocks(ctx.Text) {
		body := ctx.Text[span.contentStart:span.contentEnd]
		// Variables defined at chart-top (e.g. `{{- $policyNamespace :=
		// .Values.x -}}`) aren't visible to the parser when we parse a
		// block scalar in isolation. Pre-scan the body for `$var`
		// references that aren't declared inside the body itself, and
		// prepend phantom declarations so text/template/parse's
		// scope-check doesn't error on legitimate uses.
		augmentedBody := bodyWithPhantomVars(body)
		// Stage 1 (helm): parse only.
		if _, err := parse.Parse("ot-raw", augmentedBody, "{{", "}}", funcs); err != nil {
			emit(span, "template parse error", err.Error(), "")
			continue
		}
		// Stage 2 (hub): render stage 1, parse output with custom delims.
		// Gated behind `rules.template-syntax.layered` because Execute
		// can fail on chained-missing-keys (`.Values.foo.bar.baz`) until
		// Phase B's typed stubs make that robust.
		if !layered {
			continue
		}
		rendered, _, execErr := renderHelmStage(augmentedBody, valuesRoot, resolved, typedStubs)
		if execErr != nil {
			// Stage 1 didn't produce usable output — skip stage 2 silently.
			// Phase B will surface execute errors as typed diagnostics.
			continue
		}
		if _, err := parse.Parse("hub", rendered, "{{hub", "hub}}", hubFuncs); err != nil {
			emit(span, "hub-template parse error", err.Error(), rendered)
			continue
		}
		// Stage 2 execute: produce post-hub text for stage 3 input.
		hubData := buildHubDataContext(resolved)
		stage2Out, _, hubExecErr := renderHubStage(rendered, hubData, resolved, typedStubs)
		if hubExecErr != nil {
			// Stage 2 didn't render usable output — skip stage 3 silently.
			continue
		}
		// Stage 3 (managed): parse stage 2 output with standard delims.
		if _, err := parse.Parse("managed", stage2Out, "{{", "}}", managedFuncs); err != nil {
			emit(span, "managed-template parse error", err.Error(), stage2Out)
		}
	}
	return diagnostics
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

// mapErrLineToDocument resolves a parser-reported line number into an
// absolute document line. When the error originated in a rendered output
// (stage 2 or 3), tries to substring-match the rendered line back into
// the original block-scalar body — accurate for escape-pattern collapses
// where most line content passes through unchanged. Falls back to plain
// arithmetic mapping (`block.contentLine + (parserLine - 1)`) when no
// substring match is found, or when `renderedSrc` is empty (stage 1,
// where the parsed text IS the body and arithmetic mapping is exact).
//
// Returns the absolute document line plus the rendered context line so
// the caller can include it in the diagnostic message for user clarity.
func mapErrLineToDocument(span blockScalarSpan, body, renderedSrc string, parserLine int) (absLine int, contextLine string) {
	absLine = span.contentLine + (parserLine - 1)
	if renderedSrc == "" {
		return absLine, ""
	}
	renderedLines := splitDocLines(renderedSrc)
	idx := parserLine - 1
	// Parser reports line one past the body for EOF errors — clamp.
	if idx >= len(renderedLines) && len(renderedLines) > 0 {
		idx = len(renderedLines) - 1
	}
	if idx < 0 || idx >= len(renderedLines) {
		return absLine, ""
	}
	contextLine = strings.TrimSpace(renderedLines[idx].text)
	if contextLine == "" {
		return absLine, ""
	}
	// Substring-match into the original body. Walk body lines, find the
	// one that contains contextLine (or vice versa for collapses).
	bodyLines := splitDocLines(body)
	for i, bl := range bodyLines {
		bt := strings.TrimSpace(bl.text)
		if bt == "" {
			continue
		}
		if strings.Contains(bt, contextLine) || strings.Contains(contextLine, bt) {
			return span.contentLine + i, contextLine
		}
	}
	return absLine, contextLine
}

// varDeclRE matches `$name :=` declarations (with optional whitespace
// between `$name` and `:=`). Used to identify variables that are
// declared inside a body, so phantom declarations only get prepended
// for variables that aren't already declared locally.
var varDeclRE = regexp.MustCompile(`\$(\w+)\s*:=`)

// varRefRE matches `$name` references. Catches variables in any
// position — action body, argument list, etc.
var varRefRE = regexp.MustCompile(`\$(\w+)`)

// bodyWithPhantomVars returns the body prefixed with `{{- $name := ""
// -}}` declarations for every `$name` referenced in the body but not
// declared inside it. This makes text/template/parse's scope check
// accept block scalars that reference variables defined at chart-top
// (`{{- $policyNamespace := .Values.policy_namespace -}}` outside any
// `object-templates-raw:` block).
//
// The prepended declarations use trim markers (`{{- … -}}`) so they
// don't introduce visible whitespace or newlines — line numbers in
// the augmented body match the original 1:1, so parser-error line
// reports map back to the user's source positions cleanly.
//
// False positives are bounded: variables matching `$word` inside
// string literals or YAML scalars also get phantom declarations,
// which is harmless. False negatives only occur if the catalog or
// users introduce a variable form we don't recognize as `$word`.
func bodyWithPhantomVars(body string) string {
	declared := map[string]bool{}
	for _, m := range varDeclRE.FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	used := map[string]bool{}
	for _, m := range varRefRE.FindAllStringSubmatch(body, -1) {
		used[m[1]] = true
	}
	var prefix strings.Builder
	for name := range used {
		if declared[name] {
			continue
		}
		prefix.WriteString(`{{- $`)
		prefix.WriteString(name)
		prefix.WriteString(` := "" -}}`)
	}
	if prefix.Len() == 0 {
		return body
	}
	return prefix.String() + body
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
