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

func TestUnclosedParens_ManagedEscapeBodyUnclosed(t *testing.T) {
	// `{{ "{{" }} … {{ "}}" }}` body is parsed by the managed-cluster
	// controller after helm renders. Unmatched parens in there should
	// surface even though the body sits outside any helm `{{ … }}`.
	text := `key: '{{ "{{" }} mul ((a b) {{ "}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 unclosed-`(` from managed-escape body, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Unclosed "("`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedParens_ManagedEscapeBodyBalanced(t *testing.T) {
	text := `key: '{{ "{{" }} mul (a b) {{ "}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced parens in managed-escape body should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedParens_HubEscapeBodyUnclosed(t *testing.T) {
	text := `key: '{{ "{{hub" }} fromConfigMap (lookup "ns" "n" "x" {{ "hub}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 unclosed-`(` from hub-escape body, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Unclosed "("`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedParens_ManagedEscapeBackticked(t *testing.T) {
	// Backtick raw-string variant of the escape form — Helm renders to
	// the same runtime `{{` / `}}` as the `"..."` form, so the rule
	// must walk the body content the same way.
	text := "key: '{{ `{{` }} mul ((a b) {{ `}}` }}'"
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 unclosed-`(` from backtick managed-escape body, got %d: %+v", len(diags), diags)
	}
}

func TestUnclosedParens_HubEscapeBackticked(t *testing.T) {
	text := "key: '{{ `{{hub` }} fromConfigMap (lookup \"ns\" \"n\" \"x\" {{ `hub}}` }}'"
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 unclosed-`(` from backtick hub-escape body, got %d: %+v", len(diags), diags)
	}
}

func TestUnclosedParens_StringContainsEmbeddedHelmAction(t *testing.T) {
	// Inside a hub-escape body, a hub-level string `"…"` whose body
	// contains a helm `{{ … }}` action must not have its closing quote
	// mis-identified as one of the helm action's own string-literal
	// quotes. The action's content is opaque from the source view.
	text := `key: '{{ "{{hub" }} (index .ManagedClusterLabels "{{ $.Values.autoshiftLabelPrefix | default "autoshift.io/" }}policy-namespace") {{ "hub}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("strings with embedded helm actions inside an escape body should not produce diagnostics, got %d:\n%+v", len(diags), diags)
	}
}

func TestUnclosedParens_NestedHubEscapePairs(t *testing.T) {
	// Real-chart pattern: an outer hub-escape pair whose body contains
	// an inner hub-escape pair as one argument. Both pairs must be
	// resolved (stack-aware pairing) so the outer body scan sees the
	// matching closing paren after the inner pair.
	text := `{{ "{{hub" }} $x := (index (fromConfigMap "ns" (print {{ "{{hub" }} .ManagedClusterName {{ "hub}}" }} ".cm") "config") "key") {{ "hub}}" }}`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("nested hub-escape pairs around balanced parens should not produce diagnostics, got %d:\n%+v", len(diags), diags)
	}
}

func TestUnclosedParens_NoDoubleCountOnNestedHelmInEscape(t *testing.T) {
	// Helm action inside a managed-escape body has its own unmatched
	// `(`. The helm-level pass should report it once; the managed-body
	// pass must not report the same paren a second time.
	text := `key: '{{ "{{" }} a {{ printf (b }} {{ "}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Errorf("want exactly 1 diagnostic (no double-count), got %d: %+v", len(diags), diags)
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
