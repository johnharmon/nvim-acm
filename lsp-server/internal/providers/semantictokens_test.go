package providers

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
)

// minimal catalog: just enough to make hub-funcs and exported values
// classifiable.
func miniCatalog() catalog.Resolved {
	return catalog.Resolved{
		HubFunctions: []catalog.TemplateFunction{
			{Name: "fromSecret"},
			{Name: "lookup"},
		},
		ManagedFunctions: []catalog.TemplateFunction{
			{Name: "skipObject"},
		},
		HelmFunctions: []catalog.TemplateFunction{
			{Name: "lookup"}, // overlap with hub — should NOT be acmDistinct
		},
		SprigFunctions: []catalog.TemplateFunction{
			{Name: "default"},
		},
		GoBuiltins: []catalog.TemplateFunction{
			{Name: "index"}, {Name: "len"}, {Name: "eq"},
		},
		HubExportedValues: []catalog.ExportedValue{
			{Name: ".ManagedClusterName"},
		},
	}
}

func TestSemanticTokens_HubExpression(t *testing.T) {
	text := `foo: '{{hub fromSecret "ns" "n" "k" hub}}'`
	tokens := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if tokens == nil || len(tokens.Data) == 0 {
		t.Fatalf("expected tokens, got none")
	}
	// Tokens encode 5 uint32s each.
	if len(tokens.Data)%5 != 0 {
		t.Fatalf("expected multiple-of-5 token data, got %d", len(tokens.Data))
	}
	count := len(tokens.Data) / 5
	if count < 6 {
		t.Fatalf("expected at least 6 tokens (operators + hub keyword + fromSecret + 3 strings + close), got %d", count)
	}
}

func TestSemanticTokens_NumbersAndKeywords(t *testing.T) {
	text := `foo: '{{ if eq 1 2 }}true{{ end }}'`
	tokens := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if tokens == nil || len(tokens.Data) == 0 {
		t.Fatalf("expected tokens")
	}
	// Look for at least one number-typed token.
	foundNumber := false
	foundKeyword := false
	for i := 0; i+4 < len(tokens.Data); i += 5 {
		typeIdx := tokens.Data[i+3]
		if typeIdx == tNumber {
			foundNumber = true
		}
		if typeIdx == tKeyword {
			foundKeyword = true
		}
	}
	if !foundNumber {
		t.Errorf("expected at least one number token")
	}
	if !foundKeyword {
		t.Errorf("expected at least one keyword token (if/eq/end)")
	}
}

func TestVocabulary_OverlapExclusion(t *testing.T) {
	v := buildVocabulary(miniCatalog())
	if v.acmDistinctFuncs["lookup"] {
		t.Errorf("lookup should be excluded from acmDistinctFuncs because it's in helmFunctions")
	}
	if !v.acmDistinctFuncs["fromSecret"] {
		t.Errorf("fromSecret should be in acmDistinctFuncs")
	}
	if !v.acmDistinctFuncs["skipObject"] {
		t.Errorf("skipObject should be in acmDistinctFuncs")
	}
	if !v.knownFuncs["index"] {
		t.Errorf("index should be in knownFuncs (Go-builtin)")
	}
	if !v.acmValues["ManagedClusterName"] {
		t.Errorf("ManagedClusterName should be in acmValues")
	}
}

func TestSemanticTokens_StringContents(t *testing.T) {
	// Verify that escape-pattern inner {{ doesn't disturb the operator
	// emission for the OUTER `{{`/`}}` span delimiters — we still expect
	// exactly 4 operator-tokens of length 2 (the outer pairs).
	text := `{{ "{{hub-" }} content {{ "hub}}" }}`
	tokens := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if tokens == nil {
		t.Fatalf("expected tokens")
	}
	// Count operator tokens of length 2 (i.e., {{ or }})
	// We expect the OUTER ones only: 2 {{ + 2 }}.
	outerOps := 0
	for i := 0; i+4 < len(tokens.Data); i += 5 {
		if tokens.Data[i+3] == tOperator && tokens.Data[i+2] == 2 {
			outerOps++
		}
	}
	if outerOps != 4 {
		// Print all token records for debug
		var b strings.Builder
		for i := 0; i+4 < len(tokens.Data); i += 5 {
			b.WriteString("[")
			for j := 0; j < 5; j++ {
				if j > 0 {
					b.WriteString(",")
				}
				b.WriteString(itoa(int(tokens.Data[i+j])))
			}
			b.WriteString("] ")
		}
		t.Errorf("expected 4 length-2 operator tokens (outer brackets only), got %d. data=%s", outerOps, b.String())
	}
}

func TestSemanticTokens_StringInnerDelimiters(t *testing.T) {
	// Escape patterns that render to ACM/managed delimiters at runtime.
	// The inner `{{`/`}}` (and `{{-`/`-}}` trim variants) at the start or
	// end of a string literal's contents should emit length-2 or length-3
	// keyword tokens overlapping the string token. Outer `{{`/`}}` keep
	// their operator classification; helm/treesitter colors those.
	cases := []struct {
		name     string
		text     string
		wantKw23 int // expected count of length-2 keyword tokens at start/end of strings (i.e. inner {{ or }})
	}{
		{"hub split form", `{{ "{{hub" }} {{ "hub}}" }}`, 2},
		{"managed escape", `{{ "{{" }} body {{ "}}" }}`, 2},
		{"hub single form", `{{ "{{hub fn args hub}}" }}`, 2},
		{"trim variants", `{{ "{{-hub" }} body {{ "hub-}}" }}`, 0}, // length 3, not 2
		{"plain string, no inner delims", `{{ printf "hello world" }}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toks := SemanticTokens(SemanticTokensInput{Text: tc.text, Catalog: miniCatalog()})
			if toks == nil {
				t.Fatalf("expected tokens")
			}
			got := 0
			for i := 0; i+4 < len(toks.Data); i += 5 {
				if toks.Data[i+3] == tKeyword && toks.Data[i+2] == 2 {
					got++
				}
			}
			if got != tc.wantKw23 {
				t.Errorf("length-2 keyword tokens: got %d, want %d", got, tc.wantKw23)
			}
		})
	}
}

func TestSemanticTokens_NestedHelmInsideHubEscapeBody(t *testing.T) {
	// Real-world nesting: a helm `{{ .Values.x }}` expression sits in a
	// string literal inside the body of a hub-escape span. The inner helm
	// expression must classify as helm (operator + property tokens), not
	// as ACM-side keyword.defaultLibrary markers from the string-inner-
	// delimiter pass running over the surrounding hub-span body.
	text := `{{ "{{hub" }} fromSecret "{{ .Values.policy_namespace }}" "secret" "key" {{ "hub}}" }}`
	toks := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if toks == nil {
		t.Fatalf("expected tokens")
	}
	// Position of the inner `{{` (offset 26 in the text). It should be
	// emitted as an operator (length 2, type tOperator) by
	// appendInsideExpressions, NOT as keyword.defaultLibrary by the
	// surrounding hub-span body's string-inner-delimiter pass.
	innerOpenLine, innerOpenChar := lineColAt(text, 26)
	foundOperator := false
	conflictingKeyword := false
	for _, tok := range decodeTokens(toks.Data) {
		if tok.line == innerOpenLine && tok.startChar == innerOpenChar && tok.length == 2 {
			if tok.tokenType == tOperator {
				foundOperator = true
			}
			if tok.tokenType == tKeyword {
				conflictingKeyword = true
			}
		}
	}
	if !foundOperator {
		t.Errorf("expected inner `{{` at offset 26 to emit as tOperator (helm-level)")
	}
	if conflictingKeyword {
		t.Errorf("inner `{{` at offset 26 should NOT also emit as tKeyword (would be the bug — ACM-side classification of a helm expression)")
	}

	// `fromSecret` is between the opener escape and the inner helm
	// expression — that gap is hub-side body content and should still
	// classify as a function token.
	fromSecretLine, fromSecretChar := lineColAt(text, 14)
	foundFn := false
	for _, tok := range decodeTokens(toks.Data) {
		if tok.line == fromSecretLine && tok.startChar == fromSecretChar && tok.length == 10 && tok.tokenType == tFunction {
			foundFn = true
		}
	}
	if !foundFn {
		t.Errorf("expected fromSecret at offset 14 to still classify as function (hub-side body content outside the inner helm span)")
	}
}

type decodedTok struct {
	line, startChar uint32
	length          uint32
	tokenType       uint32
	mods            uint32
}

func decodeTokens(data []uint32) []decodedTok {
	out := []decodedTok{}
	var line, char uint32
	for i := 0; i+4 < len(data); i += 5 {
		dl := data[i]
		ds := data[i+1]
		if dl != 0 {
			line += dl
			char = ds
		} else {
			char += ds
		}
		out = append(out, decodedTok{
			line:      line,
			startChar: char,
			length:    data[i+2],
			tokenType: data[i+3],
			mods:      data[i+4],
		})
	}
	return out
}

func lineColAt(text string, offset int) (uint32, uint32) {
	var line, col uint32
	for i := 0; i < offset && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return line, col
}

func TestSemanticTokens_StringSpansHelmExprInBody(t *testing.T) {
	// User's regression: a string in a hub-escape body that contains a
	// helm expression — `"{{ $polNs }}"`. The `"…"` must emit as a single
	// tString covering the whole literal (including the helm expr bytes),
	// NOT split by the helm expression. Otherwise the body tokenizer
	// mis-pairs subsequent quotes and treats real string contents like
	// `.rendered-config` as if they were property accesses outside any
	// string.
	text := `{{ "{{hub-" }} index (fromConfigMap "{{ $polNs }}" (print .ManagedClusterName ".rendered-config") "config") {{ "hub}}" }}`
	toks := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if toks == nil {
		t.Fatalf("expected tokens")
	}

	// `.rendered-config` starts at offset 79 (length 16). It must NOT
	// appear as a tProperty token — it's inside the string literal
	// `".rendered-config"`.
	renderedLine, renderedChar := lineColAt(text, 79)
	for _, tok := range decodeTokens(toks.Data) {
		if tok.line == renderedLine && tok.startChar == renderedChar && tok.tokenType == tProperty {
			t.Errorf(`'.rendered-config' at offset 79 emitted as tProperty — it's inside a string literal, classification belongs to the surrounding tString token`)
		}
	}

	// The string `".rendered-config"` itself must emit as one tString
	// (length 18 including both quotes) starting at offset 78.
	stringLine, stringChar := lineColAt(text, 78)
	foundString := false
	for _, tok := range decodeTokens(toks.Data) {
		if tok.line == stringLine && tok.startChar == stringChar && tok.length == 18 && tok.tokenType == tString {
			foundString = true
		}
	}
	if !foundString {
		t.Errorf(`expected one tString token covering ".rendered-config" at offset 78 length 18`)
	}
}

func TestSemanticTokens_StringInnerDelimitersTrim(t *testing.T) {
	// Trim variants emit length-3 keyword tokens for `{{-` and `-}}`.
	// `hub` itself is also length-3 keyword via appendHubKeywords, so the
	// expected count includes both contributions: 2 brace runs + 2 `hub`s.
	text := `{{ "{{-hub" }} body {{ "hub-}}" }}`
	toks := SemanticTokens(SemanticTokensInput{Text: text, Catalog: miniCatalog()})
	if toks == nil {
		t.Fatalf("expected tokens")
	}
	len3 := 0
	for i := 0; i+4 < len(toks.Data); i += 5 {
		if toks.Data[i+3] == tKeyword && toks.Data[i+2] == 3 {
			len3++
		}
	}
	if len3 != 4 {
		t.Errorf("length-3 keyword tokens (2 trim braces + 2 hubs): got %d, want 4", len3)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
