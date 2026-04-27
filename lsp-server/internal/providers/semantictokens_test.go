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
	// Verify that escape-pattern inner {{ doesn't generate operator tokens
	// — the entire `"{{hub-"` should be one string token.
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
