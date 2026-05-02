package rules

import (
	"strings"
	"testing"
)

func TestUnclosedParens_BalancedNoDiag(t *testing.T) {
	text := `key: '{{ index (lookup "ns" "n") "k" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced parens should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedParens_UnclosedOpener(t *testing.T) {
	text := `key: '{{ index (lookup "ns" "n" "k" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic for unclosed `(`, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Unclosed "("`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedParens_StrayCloser(t *testing.T) {
	text := `key: '{{ index "k") }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic for stray `)`, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Stray ")"`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedParens_ParensInStringIgnored(t *testing.T) {
	text := `key: '{{ printf "(test)" "x" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("parens inside a string literal should be skipped, got: %+v", diags)
	}
}

func TestUnclosedParens_ParensInCommentIgnored(t *testing.T) {
	text := `key: '{{/* (foo bar) */ printf "x" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("parens inside a `/* … */` comment should be skipped, got: %+v", diags)
	}
}

func TestUnclosedParens_DirectHubFormBalanced(t *testing.T) {
	text := `key: '{{hub fromConfigMap (lookup "ns" "n") "n" "k" hub}}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced parens in direct hub form should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedParens_MultipleOrphansOneSpan(t *testing.T) {
	text := `key: '{{ ((( }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 3 {
		t.Errorf("want 3 unclosed-`(` diagnostics, got %d: %+v", len(diags), diags)
	}
}

func TestUnclosedParens_PerSpanScope(t *testing.T) {
	// `(` opened in one action and `)` in another don't pair — each
	// span's parens are evaluated independently.
	text := `key: '{{ printf (}} something {{ ) }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	// First span: unclosed `(`. Second span: stray `)`. Both fire.
	if len(diags) != 2 {
		t.Errorf("want 2 diagnostics (one per span), got %d: %+v", len(diags), diags)
	}
}

func TestUnclosedParens_DisabledByConfig(t *testing.T) {
	text := `key: '{{ ( }}'`
	settings := Settings{
		"rules": map[string]any{"unclosed-parens": map[string]any{"enabled": false}},
	}
	diags := unclosedParens{}.Run(Context{Text: text, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("rule disabled but produced diagnostics: %+v", diags)
	}
}
