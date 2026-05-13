package rules

import (
	"testing"
)

func TestUnclosedParens_RealChartManagedEscapeWithEmbeddedHelm(t *testing.T) {
	text := `name: '{{ "{{" }} with (lookup "operators.coreos.com/v1" "OperatorGroup" "{{ .Values.quay.namespace }}" "").items {{ "}}" }}{{ "{{" }} (index . 0).metadata.name {{ "}}" }}{{ "{{" }} else {{ "}}" }}{{ .Values.quay.namespace }}{{ "{{" }} end {{ "}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d:\n%+v", len(diags), diags)
	}
}

func TestUnclosedParens_RealChartHubEscapeWithEmbeddedHelm(t *testing.T) {
	text := `source: '{{ "{{hub" }} $base := (index .ManagedClusterLabels "autoshift.io/quay-source" | default "{{ .Values.quay.source }}") {{ "hub}}" }}{{ "{{hub" }} ternary (printf "%s-%s" $base (index .ManagedClusterLabels "autoshift.io/mirror-catalog-suffix" | default "mirror")) $base (eq (index .ManagedClusterLabels "autoshift.io/disconnected-mirror" | default "false") "true") {{ "hub}}" }}'`
	diags := unclosedParens{}.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d:\n%+v", len(diags), diags)
	}
}
