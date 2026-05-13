package rules

import (
	"strings"
	"testing"
)

func TestUnclosedDelimiters_BalancedNoDiag(t *testing.T) {
	text := `apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: example
spec:
  configurationPolicy:
    object-templates-raw: |
      data:
        key: '{{hub fromConfigMap "" "cm" "k" hub}}'
        n: {{ printf "%d" 42 }}
`
	diags := unclosedDelimiters{}.Run(Context{
		Text:     text,
		Settings: Settings{},
	})
	if len(diags) != 0 {
		t.Errorf("balanced doc should produce no diagnostics, got %d: %+v", len(diags), diags)
	}
}

func TestUnclosedDelimiters_OpenWithoutClose(t *testing.T) {
	text := `key: {{ printf "x"`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Message, `Unclosed go-template delimiter`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedDelimiters_StrayClose(t *testing.T) {
	text := `key: foo }} bar`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Stray closing delimiter`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedDelimiters_HubOpenerOrphan(t *testing.T) {
	// Direct `{{hub` opener never gets a `hub}}` closer.
	text := `data: '{{hub fromConfigMap "" "cm" "k"}}'`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	hubDiag := false
	for _, d := range diags {
		if strings.Contains(d.Message, `Hub-template "{{hub"`) {
			hubDiag = true
		}
	}
	if !hubDiag {
		t.Errorf("expected an orphan-{{hub diagnostic, got %+v", diags)
	}
}

func TestUnclosedDelimiters_HubCloserOrphan(t *testing.T) {
	// `hub}}` with no preceding `{{hub`.
	text := `data: 'fromConfigMap "" "cm" "k" hub}}'`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	hubDiag := false
	for _, d := range diags {
		if strings.Contains(d.Message, `Hub-template "hub}}"`) {
			hubDiag = true
		}
	}
	if !hubDiag {
		t.Errorf("expected an orphan-hub}} diagnostic, got %+v", diags)
	}
}

func TestUnclosedDelimiters_EscapeFormBalanced(t *testing.T) {
	// Hub-template escape form: `{{hub` and `hub}}` only appear inside
	// string literals — those should be skipped, leaving zero diagnostics.
	text := `data: |
  key: {{ "{{hub fromConfigMap \"\" \"cm\" \"k\" hub}}" }}
`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("escape-form balanced doc should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_UnclosedBeforeBalancedExpr(t *testing.T) {
	// Regression: an unclosed `{{` earlier in the file used to silently
	// steal the close of a later balanced expression with the old greedy-
	// pairing scanner, hiding the imbalance entirely.
	text := `line1: {{
line2: {{ valid }}
`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic for the unclosed line1 {{, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `Unclosed go-template delimiter`) {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
	if diags[0].Range.Start.Line != 0 {
		t.Errorf("diagnostic should fire on line 0, got line %d", diags[0].Range.Start.Line)
	}
}

func TestUnclosedDelimiters_PartialEscapeFormFires(t *testing.T) {
	// Mid-typing the managed-escape pattern. Two failures show up:
	//   - the second helm `{{` (in `{{ "}}"`) never closes — go-template
	//     layer reports unclosed `{{`
	//   - the managed-escape opener `{{ "{{" }}` has no closing
	//     `{{ "}}" }}` — escape-pair layer reports orphan opener
	text := `data: |
  {{ "{{" }} $myValue := "test" {{ "}}"
`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 2 {
		t.Fatalf("want 2 diagnostics (unclosed {{ + orphan managed-escape opener), got %d: %+v", len(diags), diags)
	}
	gotMsgs := []string{diags[0].Message, diags[1].Message}
	wantSubstrs := []string{`Unclosed go-template delimiter`, `Managed-escape opener`}
	for _, want := range wantSubstrs {
		found := false
		for _, m := range gotMsgs {
			if strings.Contains(m, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing diagnostic containing %q in %+v", want, gotMsgs)
		}
	}
}

func TestUnclosedDelimiters_BalancedManagedEscape(t *testing.T) {
	// User's original case: a balanced managed-escape line should produce
	// zero diagnostics.
	text := `{{ "{{" }} $myValue := "test" {{ "}}" }}`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced managed-escape should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_OrphanManagedEscapeCloser(t *testing.T) {
	text := `prefix {{ "}}" }} suffix`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `Managed-escape closer`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a managed-escape orphan-closer diagnostic, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_BalancedManagedEscape_Backtick(t *testing.T) {
	// Same as TestUnclosedDelimiters_BalancedManagedEscape but using
	// backtick raw-string literals in the escape forms — Helm renders
	// them to the same value as `"..."` so the diagnostic must treat
	// the two as equivalent.
	text := "{{ `{{` }} $myValue := \"test\" {{ `}}` }}"
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced backtick managed-escape should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_BalancedHubEscape_Backtick(t *testing.T) {
	text := "{{ `{{hub` }} fromSecret \"a\" \"b\" \"c\" {{ `hub}}` }}"
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced backtick hub-escape should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_NestedEscapePairs(t *testing.T) {
	// Real-chart pattern: outer escape pair whose body contains an
	// inner escape pair. Stack-aware pairing should resolve both with
	// zero diagnostics; a flat single-slot state machine would
	// misreport the inner pair as orphaning the outer opener.
	text := `key: '{{ "{{hub" }} a {{ "{{hub" }} inner {{ "hub}}" }} b {{ "hub}}" }}'`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	for _, d := range diags {
		if strings.Contains(d.Message, "Hub-escape") {
			t.Errorf("nested hub-escape pairs should not produce a Hub-escape diagnostic, got: %+v", d)
		}
	}
}

func TestUnclosedDelimiters_NestedManagedEscapePairs(t *testing.T) {
	text := `key: '{{ "{{" }} a {{ "{{" }} inner {{ "}}" }} b {{ "}}" }}'`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	for _, d := range diags {
		if strings.Contains(d.Message, "Managed-escape") {
			t.Errorf("nested managed-escape pairs should not produce a Managed-escape diagnostic, got: %+v", d)
		}
	}
}

func TestUnclosedDelimiters_OrphanHubEscapePair(t *testing.T) {
	// Hub-escape opener with no matching closer.
	text := `prefix {{ "{{hub" }} body without closer`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `Hub-escape opener`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a hub-escape orphan-opener diagnostic, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_SingleLineComment(t *testing.T) {
	text := `key: '{{/* a comment */}}'`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced single-line comment should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_MultiLineComment(t *testing.T) {
	text := `data: |
  {{/* multi-line
       comment block
       with several lines */}}
`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("balanced multi-line comment should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_CommentContainingBraces(t *testing.T) {
	// A comment whose body contains `{{` and `}}` literals must not
	// trip the open/close state machine.
	text := `{{/* talks about {{hub fn args hub}} and {{ printf "x" }} */}}`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("comment body containing {{/}} literals should not produce diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_TrimMarkerComment(t *testing.T) {
	// Trim variants `{{- /* ... */ -}}` should also pair correctly.
	text := `{{- /* trimmed comment */ -}}`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("trim-marker comment should produce no diagnostics, got: %+v", diags)
	}
}

func TestUnclosedDelimiters_UnterminatedComment(t *testing.T) {
	// `{{/* without */}}` — comment never terminates, so the `{{` is
	// unclosed at EOF. Must surface a diagnostic.
	text := `{{/* this comment never closes`
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic for unclosed `{{` containing unterminated comment, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "Unclosed go-template delimiter") {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestUnclosedDelimiters_DisabledByConfig(t *testing.T) {
	text := `key: {{ printf "x"`
	settings := Settings{"rules": map[string]any{"unclosed-delimiters": map[string]any{"enabled": false}}}
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("rule disabled but produced diagnostics: %+v", diags)
	}
}
