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

func TestUnclosedDelimiters_DisabledByConfig(t *testing.T) {
	text := `key: {{ printf "x"`
	settings := Settings{"rules": map[string]any{"unclosed-delimiters": map[string]any{"enabled": false}}}
	diags := unclosedDelimiters{}.Run(Context{Text: text, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("rule disabled but produced diagnostics: %+v", diags)
	}
}
