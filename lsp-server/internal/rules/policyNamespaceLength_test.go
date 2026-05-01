package rules

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/parsedoc"
)

func TestPolicyNamespaceLength_OverDefaultFires(t *testing.T) {
	// Default max is 20.
	text := `apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: ok
  namespace: this-namespace-name-is-way-too-long
`
	docs := parsedoc.ParseAll(text)
	diags := policyNamespaceLength{}.Run(Context{
		Text: text, Docs: docs, Settings: Settings{},
	})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic for over-length namespace, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "namespace") {
		t.Errorf("message should mention namespace; got: %q", diags[0].Message)
	}
}

func TestPolicyNamespaceLength_AtBoundaryDoesNotFire(t *testing.T) {
	// Exactly 20 chars — at the boundary, must NOT fire (rule uses
	// strict greater-than via `len > maxLength`).
	text := `kind: Policy
metadata:
  name: ok
  namespace: 12345678901234567890
`
	docs := parsedoc.ParseAll(text)
	diags := policyNamespaceLength{}.Run(Context{Text: text, Docs: docs, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("namespace exactly at boundary (20) should not fire; got: %+v", diags)
	}
}

func TestPolicyNamespaceLength_NoNamespaceDoesNotFire(t *testing.T) {
	// Cluster-scoped or namespace-implicit resources have no
	// metadata.namespace — rule must skip them.
	text := `kind: Policy
metadata:
  name: clusterscoped
`
	docs := parsedoc.ParseAll(text)
	diags := policyNamespaceLength{}.Run(Context{Text: text, Docs: docs, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("missing namespace should be silently skipped; got: %+v", diags)
	}
}

func TestPolicyNamespaceLength_KindNotInListDoesNotFire(t *testing.T) {
	text := `kind: ConfigMap
metadata:
  name: x
  namespace: this-namespace-is-very-long-and-should-not-fire-because-of-kind
`
	docs := parsedoc.ParseAll(text)
	diags := policyNamespaceLength{}.Run(Context{Text: text, Docs: docs, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("ConfigMap isn't in the kinds list; should not fire. got: %+v", diags)
	}
}

func TestPolicyNamespaceLength_ConfigurableMaxLength(t *testing.T) {
	text := `kind: Policy
metadata:
  name: x
  namespace: short-ns
`
	docs := parsedoc.ParseAll(text)
	settings := Settings{
		"rules": map[string]any{
			"policy-namespace-length": map[string]any{"maxLength": float64(5)},
		},
	}
	diags := policyNamespaceLength{}.Run(Context{Text: text, Docs: docs, Settings: settings})
	if len(diags) != 1 {
		t.Errorf("with maxLength=5, `short-ns` (8 chars) should fire; got %d diagnostics", len(diags))
	}
}

func TestPolicyNamespaceLength_DisabledByConfig(t *testing.T) {
	text := `kind: Policy
metadata:
  name: x
  namespace: this-is-way-too-long-for-the-default-of-20
`
	docs := parsedoc.ParseAll(text)
	settings := Settings{
		"rules": map[string]any{
			"policy-namespace-length": map[string]any{"enabled": false},
		},
	}
	diags := policyNamespaceLength{}.Run(Context{Text: text, Docs: docs, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("rule disabled but produced diagnostics: %+v", diags)
	}
}
