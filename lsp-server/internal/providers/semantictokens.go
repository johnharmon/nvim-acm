package providers

import (
	"regexp"
	"sort"
	"strings"

	"github.com/autoshift/lsp-server/internal/catalog"
	"github.com/autoshift/lsp-server/internal/context"
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
// string literals so `}}` inside `"..."` doesn't close the expression.
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
	{regexp.MustCompile(`\{\{-?\s*"\{\{(hub)\b`), 1},
	{regexp.MustCompile(`\b(hub)\}\}"`), 1},
}

func appendHubKeywords(tokens []rawToken, text string) []rawToken {
	for _, p := range hubKeywordPatterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(text, -1) {
			off := m[2*p.captureIdx]
			length := m[2*p.captureIdx+1] - off
			tokens = append(tokens, rawToken{offset: off, length: length, tokenType: tKeyword})
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
	for _, span := range context.FindHubSpans(text) {
		if span.ContentEnd <= span.ContentStart {
			continue
		}
		inner := text[span.ContentStart:span.ContentEnd]
		tokens = tokenizeContent(tokens, inner, span.ContentStart, vocab)
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
