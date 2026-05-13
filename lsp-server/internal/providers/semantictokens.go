package providers

import (
	"regexp"
	"sort"
	"strings"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// SemanticTokens legend. Order is significant — clients use the indexes for
// the token type and bit positions for modifiers.
var (
	semanticTokenTypes = []string{
		"function", "variable", "keyword", "property",
		"string", "number", "operator", "comment",
	}
	semanticTokenModifiers = []string{"defaultLibrary", "readonly"}
)

// Legend exposes the token legend so the server can advertise it during
// initialize.
func Legend() protocol.SemanticTokensLegend {
	return protocol.SemanticTokensLegend{
		TokenTypes:     semanticTokenTypes,
		TokenModifiers: semanticTokenModifiers,
	}
}

// Token-type indexes.
const (
	tFunction uint32 = iota
	tVariable
	tKeyword
	tProperty
	tString
	tNumber
	tOperator
	tComment
)

// Modifier bitmask values.
const (
	mDefaultLibrary uint32 = 1 << 0
	mReadonly       uint32 = 1 << 1
)

// SemanticTokensInput packages everything the provider needs.
type SemanticTokensInput struct {
	Text    string
	Catalog catalog.Resolved
}

// SemanticTokens emits a full document semantic-tokens response.
func SemanticTokens(in SemanticTokensInput) *protocol.SemanticTokens {
	vocab := buildVocabulary(in.Catalog)
	tokens := []rawToken{}
	tokens = appendHubKeywords(tokens, in.Text)
	tokens = appendInsideExpressions(tokens, in.Text, vocab)
	tokens = appendInsideHubSpans(tokens, in.Text, vocab)
	tokens = appendInsideManagedSpans(tokens, in.Text, vocab)

	sort.Slice(tokens, func(i, j int) bool { return tokens[i].offset < tokens[j].offset })
	tokens = dedupeByOffset(tokens)

	data := encodeTokens(tokens, in.Text)
	return &protocol.SemanticTokens{Data: data}
}

// rawToken holds a token before delta encoding.
type rawToken struct {
	offset    int
	length    int
	tokenType uint32
	modifiers uint32
}

type vocabulary struct {
	acmDistinctFuncs map[string]bool
	knownFuncs       map[string]bool
	acmValues        map[string]bool
}

func buildVocabulary(c catalog.Resolved) vocabulary {
	overlap := map[string]bool{}
	for _, f := range c.SprigFunctions {
		overlap[f.Name] = true
	}
	for _, f := range c.GoBuiltins {
		overlap[f.Name] = true
	}
	for _, f := range c.HelmFunctions {
		overlap[f.Name] = true
	}

	known := map[string]bool{}
	for _, f := range c.HelmFunctions {
		known[f.Name] = true
	}
	for _, f := range c.SprigFunctions {
		known[f.Name] = true
	}
	for _, f := range c.GoBuiltins {
		known[f.Name] = true
	}
	for _, f := range c.HubFunctions {
		known[f.Name] = true
	}
	for _, f := range c.ManagedFunctions {
		known[f.Name] = true
	}

	acm := map[string]bool{}
	for _, f := range c.HubFunctions {
		if !overlap[f.Name] {
			acm[f.Name] = true
		}
	}
	for _, f := range c.ManagedFunctions {
		if !overlap[f.Name] {
			acm[f.Name] = true
		}
	}

	values := map[string]bool{}
	for _, v := range c.HubExportedValues {
		values[strings.TrimPrefix(v.Name, ".")] = true
	}
	for _, v := range c.ManagedExportedValues {
		values[strings.TrimPrefix(v.Name, ".")] = true
	}
	return vocabulary{acmDistinctFuncs: acm, knownFuncs: known, acmValues: values}
}

// findExpressionSpans scans text and returns each `{{...}}` span, respecting
// string literals so `}}` inside `"..."` doesn't close the expression and
// `/* … */` go-template comments so `}}` (or `{{`) embedded in comment
// bodies don't falsely terminate the span.
func findExpressionSpans(text string) []expressionSpan {
	spans := []expressionSpan{}
	i := 0
	for i < len(text)-1 {
		if text[i] != '{' || text[i+1] != '{' {
			i++
			continue
		}
		spanStart := i
		openLen := 2
		i += 2
		if i < len(text) && text[i] == '-' {
			openLen++
			i++
		}
		inString := false
		var stringChar byte
		closed := false
		for i < len(text) {
			c := text[i]
			if inString {
				if c == '\\' && stringChar == '"' && i+1 < len(text) {
					i += 2
					continue
				}
				if c == stringChar {
					inString = false
				}
				i++
				continue
			}
			if c == '"' || c == '`' {
				inString = true
				stringChar = c
				i++
				continue
			}
			if c == '/' && i+1 < len(text) && text[i+1] == '*' {
				i += 2
				for i+1 < len(text) && !(text[i] == '*' && text[i+1] == '/') {
					i++
				}
				if i+1 < len(text) {
					i += 2
				} else {
					i = len(text)
				}
				continue
			}
			if c == '-' && i+2 < len(text) && text[i+1] == '}' && text[i+2] == '}' {
				spans = append(spans, expressionSpan{start: spanStart, end: i + 3, openLen: openLen, closeLen: 3})
				i += 3
				closed = true
				break
			}
			if c == '}' && i+1 < len(text) && text[i+1] == '}' {
				spans = append(spans, expressionSpan{start: spanStart, end: i + 2, openLen: openLen, closeLen: 2})
				i += 2
				closed = true
				break
			}
			i++
		}
		if !closed {
			break
		}
	}
	return spans
}

type expressionSpan struct {
	start    int
	end      int
	openLen  int
	closeLen int
}

var hubKeywordPatterns = []struct {
	re         *regexp.Regexp
	captureIdx int
}{
	{regexp.MustCompile(`\{\{-?\s*(hub)\b`), 1},
	{regexp.MustCompile(`\b(hub)\s*-?\}\}`), 1},
	{regexp.MustCompile(`\{\{-?\s*[\x60"]\{\{(hub)\b`), 1},
	{regexp.MustCompile(`\b(hub)\}\}[\x60"]`), 1},
}

// appendStringInnerDelims tags `{{`/`{{-` and `}}`/`-}}` runs that sit at the
// very start or end of a string literal's contents. Used by ACM escape
// patterns the chart renders into runtime template delimiters:
//
//	{{ "{{hub" }} ... {{ "hub}}" }}     hub escape, split form
//	{{ "{{hub fn args hub}}" }}         hub escape, single form
//	{{ "{{" }} ... {{ "}}" }}           managed escape
//
// The whole literal is still a tString token (already emitted by the caller);
// these overlap as keyword hints so the brace runs read alongside `hub` /
// `if` / `range`. quoteStart/quoteEnd are the byte offsets *within `inner`*
// of the opening quote and one past the closing quote.
//
// realHelmRanges (absolute document offsets, sorted by start) lets the
// caller suppress emission when the inner `{{` is the start of an actual
// helm expression (e.g. `"{{ .Values.x }}"` in a hub-span body) rather
// than an escape-form runtime-delimiter pattern. The helm expression's
// own tokens already classify those bytes; tagging them again as
// keyword.defaultLibrary would mis-color a real helm `{{`/`}}` as if it
// were a managed-escape inner marker. Pass nil if no suppression is
// needed (e.g. tokenization inside a single helm expression body).
func appendStringInnerDelims(tokens []rawToken, inner string, innerStart, quoteStart, quoteEnd int, realHelmRanges [][2]int) []rawToken {
	contentStart := quoteStart + 1
	contentEnd := quoteEnd - 1
	if contentEnd <= contentStart {
		return tokens
	}
	isHelmStart := func(absOff int) bool {
		for _, r := range realHelmRanges {
			if r[0] == absOff {
				return true
			}
			if r[0] > absOff {
				break
			}
		}
		return false
	}
	isHelmEnd := func(absOff int) bool {
		for _, r := range realHelmRanges {
			if r[1] == absOff {
				return true
			}
			if r[0] > absOff {
				break
			}
		}
		return false
	}
	// Same defaultLibrary modifier as appendHubKeywords: these are ACM-side
	// delimiter runs (the `{{`/`}}` that helm renders into runtime markers
	// for the managed cluster), not go-template control keywords.
	if n := openDelimRun(inner, contentStart, contentEnd); n > 0 {
		absOpen := innerStart + contentStart
		if !isHelmStart(absOpen) {
			tokens = append(tokens, rawToken{offset: absOpen, length: n, tokenType: tKeyword, modifiers: mDefaultLibrary})
		}
	}
	if n := closeDelimRun(inner, contentStart, contentEnd); n > 0 {
		absCloseStart := innerStart + contentEnd - n
		absCloseEnd := innerStart + contentEnd
		if !isHelmEnd(absCloseEnd) {
			tokens = append(tokens, rawToken{offset: absCloseStart, length: n, tokenType: tKeyword, modifiers: mDefaultLibrary})
		}
	}
	return tokens
}

func openDelimRun(s string, start, end int) int {
	if end-start < 2 || s[start] != '{' || s[start+1] != '{' {
		return 0
	}
	if end-start >= 3 && s[start+2] == '-' {
		return 3
	}
	return 2
}

func closeDelimRun(s string, start, end int) int {
	if end-start < 2 || s[end-1] != '}' || s[end-2] != '}' {
		return 0
	}
	if end-start >= 3 && s[end-3] == '-' {
		return 3
	}
	return 2
}

func appendHubKeywords(tokens []rawToken, text string) []rawToken {
	for _, p := range hubKeywordPatterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(text, -1) {
			off := m[2*p.captureIdx]
			length := m[2*p.captureIdx+1] - off
			// `defaultLibrary` modifier marks this as an ACM-side keyword
			// (not a go-template keyword like `if`/`range`), so colorschemes
			// can target @lsp.type.keyword.defaultLibrary.* separately.
			tokens = append(tokens, rawToken{offset: off, length: length, tokenType: tKeyword, modifiers: mDefaultLibrary})
		}
	}
	return tokens
}

func appendInsideExpressions(tokens []rawToken, text string, vocab vocabulary) []rawToken {
	for _, sp := range findExpressionSpans(text) {
		tokens = append(tokens, rawToken{offset: sp.start, length: sp.openLen, tokenType: tOperator})
		tokens = append(tokens, rawToken{offset: sp.end - sp.closeLen, length: sp.closeLen, tokenType: tOperator})

		innerStart := sp.start + sp.openLen
		innerEnd := sp.end - sp.closeLen
		inner := text[innerStart:innerEnd]
		trimmed := strings.TrimSpace(inner)
		if strings.HasPrefix(trimmed, "/*") && strings.HasSuffix(trimmed, "*/") {
			tokens = append(tokens, rawToken{offset: innerStart, length: len(inner), tokenType: tComment})
			continue
		}
		tokens = tokenizeContent(tokens, inner, innerStart, vocab)
	}
	return tokens
}

func appendInsideHubSpans(tokens []rawToken, text string, vocab vocabulary) []rawToken {
	helmSpans := findExpressionSpans(text)
	for _, span := range context.FindHubSpans(text) {
		if span.ContentEnd <= span.ContentStart {
			continue
		}
		tokens = tokenizeBodySkippingHelm(tokens, text, helmSpans, span.ContentStart, span.ContentEnd, vocab)
	}
	return tokens
}

func appendInsideManagedSpans(tokens []rawToken, text string, vocab vocabulary) []rawToken {
	helmSpans := findExpressionSpans(text)
	for _, span := range context.FindManagedSpans(text) {
		if span.ContentEnd <= span.ContentStart {
			continue
		}
		tokens = tokenizeBodySkippingHelm(tokens, text, helmSpans, span.ContentStart, span.ContentEnd, vocab)
	}
	return tokens
}

// tokenizeBodySkippingHelm tokenizes a hub/managed-span body and treats
// embedded helm expressions as transparent — their bytes are already
// classified by appendInsideExpressions, so we don't emit anything new
// for them, but we also don't let them break the surrounding tokenizer
// state. In particular, a string `"{{ .Values.x }}"` containing a helm
// expression must still emit as a single tString covering the whole
// literal (the inner `{{ … }}` is text-content from the tokenizer's
// perspective, the helm-level classification overlays it).
//
// Splitting the body into gaps and tokenizing each gap independently
// (the previous approach) breaks string state at gap boundaries: an
// unterminated `"` at the end of a gap is silently dropped and the next
// gap starts a *new* string, which mis-pairs with whatever closing `"`
// happens to come next and leaves real content (function names,
// `.foo` paths) outside any string token — which is the bug that makes
// `.rendered-config` inside `".rendered-config"` light up as a property.
//
// helmSpans must be sorted by start offset (findExpressionSpans returns
// them that way) and non-overlapping.
func tokenizeBodySkippingHelm(tokens []rawToken, text string, helmSpans []expressionSpan, contentStart, contentEnd int, vocab vocabulary) []rawToken {
	skips := [][2]int{}
	for _, e := range helmSpans {
		if e.end <= contentStart {
			continue
		}
		if e.start >= contentEnd {
			break
		}
		s := e.start
		if s < contentStart {
			s = contentStart
		}
		en := e.end
		if en > contentEnd {
			en = contentEnd
		}
		skips = append(skips, [2]int{s, en})
	}
	return tokenizeContentWithSkips(tokens, text[contentStart:contentEnd], contentStart, vocab, skips)
}

// tokenizeContentWithSkips behaves like tokenizeContent except that any
// byte range listed in `skips` (absolute document offsets) is skipped
// over without emitting tokens *or* breaking string-scan state. The
// outer walk and the inside-string scans both honor the skip list.
//
// skips are absolute offsets relative to the document, sorted by start,
// non-overlapping. innerStart is the absolute offset of `inner[0]` in
// the document (so localOffset + innerStart gives the absolute offset).
func tokenizeContentWithSkips(tokens []rawToken, inner string, innerStart int, vocab vocabulary, skips [][2]int) []rawToken {
	skipFromLocal := func(local int) (int, bool) {
		abs := innerStart + local
		for _, r := range skips {
			if r[0] == abs {
				return r[1] - innerStart, true
			}
			if r[0] > abs {
				break
			}
		}
		return 0, false
	}

	n := len(inner)
	i := 0
	for i < n {
		if to, ok := skipFromLocal(i); ok {
			i = to
			continue
		}
		c := inner[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"':
			start := i
			i++
			for i < n && inner[i] != '"' {
				if to, ok := skipFromLocal(i); ok {
					i = to
					continue
				}
				if inner[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i < n {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tString})
			tokens = appendStringInnerDelims(tokens, inner, innerStart, start, i, skips)
		case c == '`':
			start := i
			i++
			for i < n && inner[i] != '`' {
				if to, ok := skipFromLocal(i); ok {
					i = to
					continue
				}
				i++
			}
			if i < n {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tString})
			tokens = appendStringInnerDelims(tokens, inner, innerStart, start, i, skips)
		case isDigit(c):
			start := i
			for i < n && (isDigit(inner[i]) || inner[i] == '.') {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tNumber})
		case c == '$':
			start := i
			i++
			for i < n && isWord(inner[i]) {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tVariable})
		case c == '.':
			start := i
			i++
			if i < n && isIdentStartByte(inner[i]) {
				for i < n && isWord(inner[i]) {
					i++
				}
				name := inner[start+1 : i]
				mods := uint32(0)
				if vocab.acmValues[name] {
					mods = mDefaultLibrary | mReadonly
				}
				tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tProperty, modifiers: mods})
			} else {
				tokens = append(tokens, rawToken{offset: innerStart + start, length: 1, tokenType: tOperator})
			}
		case isIdentStartByte(c):
			start := i
			for i < n && isWord(inner[i]) {
				i++
			}
			name := inner[start:i]
			tokens = classifyIdent(tokens, innerStart+start, name, vocab)
		case c == '(' || c == ')' || c == '[' || c == ']' || c == '|' || c == ',':
			tokens = append(tokens, rawToken{offset: innerStart + i, length: 1, tokenType: tOperator})
			i++
		case c == ':' && i+1 < n && inner[i+1] == '=':
			tokens = append(tokens, rawToken{offset: innerStart + i, length: 2, tokenType: tOperator})
			i += 2
		default:
			i++
		}
	}
	return tokens
}

func tokenizeContent(tokens []rawToken, inner string, innerStart int, vocab vocabulary) []rawToken {
	i := 0
	for i < len(inner) {
		c := inner[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"':
			start := i
			i++
			for i < len(inner) && inner[i] != '"' {
				if inner[i] == '\\' && i+1 < len(inner) {
					i += 2
					continue
				}
				i++
			}
			if i < len(inner) {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tString})
			tokens = appendStringInnerDelims(tokens, inner, innerStart, start, i, nil)
		case c == '`':
			start := i
			i++
			for i < len(inner) && inner[i] != '`' {
				i++
			}
			if i < len(inner) {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tString})
			tokens = appendStringInnerDelims(tokens, inner, innerStart, start, i, nil)
		case isDigit(c):
			start := i
			for i < len(inner) && (isDigit(inner[i]) || inner[i] == '.') {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tNumber})
		case c == '$':
			start := i
			i++
			for i < len(inner) && isWord(inner[i]) {
				i++
			}
			tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tVariable})
		case c == '.':
			start := i
			i++
			if i < len(inner) && isIdentStartByte(inner[i]) {
				for i < len(inner) && isWord(inner[i]) {
					i++
				}
				name := inner[start+1 : i]
				mods := uint32(0)
				if vocab.acmValues[name] {
					mods = mDefaultLibrary | mReadonly
				}
				tokens = append(tokens, rawToken{offset: innerStart + start, length: i - start, tokenType: tProperty, modifiers: mods})
			} else {
				tokens = append(tokens, rawToken{offset: innerStart + start, length: 1, tokenType: tOperator})
			}
		case isIdentStartByte(c):
			start := i
			for i < len(inner) && isWord(inner[i]) {
				i++
			}
			name := inner[start:i]
			tokens = classifyIdent(tokens, innerStart+start, name, vocab)
		case c == '(' || c == ')' || c == '[' || c == ']' || c == '|' || c == ',':
			tokens = append(tokens, rawToken{offset: innerStart + i, length: 1, tokenType: tOperator})
			i++
		case c == ':' && i+1 < len(inner) && inner[i+1] == '=':
			tokens = append(tokens, rawToken{offset: innerStart + i, length: 2, tokenType: tOperator})
			i += 2
		default:
			i++
		}
	}
	return tokens
}

var (
	controlKeywords  = strSet("if", "else", "end", "range", "with", "template", "define", "block", "return", "break", "continue")
	operatorKeywords = strSet("eq", "ne", "lt", "le", "gt", "ge", "and", "or", "not")
	literalKeywords  = strSet("true", "false", "nil")
)

func classifyIdent(tokens []rawToken, offset int, name string, vocab vocabulary) []rawToken {
	if name == "hub" {
		return tokens // emitted by appendHubKeywords
	}
	if controlKeywords[name] || operatorKeywords[name] || literalKeywords[name] {
		return append(tokens, rawToken{offset: offset, length: len(name), tokenType: tKeyword})
	}
	if vocab.acmDistinctFuncs[name] {
		return append(tokens, rawToken{offset: offset, length: len(name), tokenType: tFunction, modifiers: mDefaultLibrary})
	}
	if vocab.knownFuncs[name] {
		return append(tokens, rawToken{offset: offset, length: len(name), tokenType: tFunction})
	}
	return tokens
}

func encodeTokens(tokens []rawToken, text string) []uint32 {
	out := make([]uint32, 0, len(tokens)*5)
	prevLine := uint32(0)
	prevChar := uint32(0)
	for _, t := range tokens {
		if t.length <= 0 {
			continue
		}
		startPos := positionAt(text, t.offset)
		endPos := positionAt(text, t.offset+t.length)
		if startPos.Line != endPos.Line {
			continue
		}
		deltaLine := startPos.Line - prevLine
		deltaStart := startPos.Character
		if deltaLine == 0 {
			deltaStart = startPos.Character - prevChar
		}
		out = append(out, deltaLine, deltaStart, uint32(t.length), t.tokenType, t.modifiers)
		prevLine = startPos.Line
		prevChar = startPos.Character
	}
	return out
}

func dedupeByOffset(tokens []rawToken) []rawToken {
	if len(tokens) <= 1 {
		return tokens
	}
	out := tokens[:0]
	last := rawToken{offset: -1}
	for _, t := range tokens {
		if t.offset == last.offset && t.length == last.length && t.tokenType == last.tokenType {
			continue
		}
		out = append(out, t)
		last = t
	}
	return out
}

func isDigit(c byte) bool        { return c >= '0' && c <= '9' }
func isIdentStartByte(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}
func isWord(c byte) bool { return isIdentStartByte(c) || isDigit(c) }

func strSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, x := range items {
		m[x] = true
	}
	return m
}
