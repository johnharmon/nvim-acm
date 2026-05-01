package rules

import (
	"fmt"

	"github.com/acm-ls/lsp-server/internal/parsedoc"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type policyNamespaceLength struct{}

func (policyNamespaceLength) ID() string { return "policy-namespace-length" }

// Run flags Policy / PlacementBinding / etc. whose `metadata.namespace`
// exceeds the configured maximum. Default is 20 to match the typical
// enterprise CI gate; configurable via
// `rules.policy-namespace-length.maxLength` and the `kinds` list.
//
// Documents without a `metadata.namespace` (cluster-scoped resources or
// namespace-implicit deployments) are silently skipped — the rule only
// fires when a namespace is explicitly declared and too long.
func (policyNamespaceLength) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.policy-namespace-length.enabled", true) {
		return nil
	}
	maxLength := GetInt(ctx.Settings, "rules.policy-namespace-length.maxLength", 20)
	severity := Severity(Get(ctx.Settings, "rules.policy-namespace-length.severity", string(SeverityWarning)))
	defaultKinds := []string{
		"Policy", "PlacementBinding", "PlacementRule", "Placement",
		"ConfigurationPolicy", "OperatorPolicy",
	}
	kinds := setOf(GetStringSlice(ctx.Settings, "rules.policy-namespace-length.kinds", defaultKinds))

	out := []protocol.Diagnostic{}
	for _, d := range ctx.Docs {
		if d.Kind == "" || d.Namespace == "" {
			continue
		}
		if !kinds[d.Kind] {
			continue
		}
		if len(d.Namespace) <= maxLength {
			continue
		}
		sev := severity.ToLSP()
		code := protocol.IntegerOrString{Value: "policy-namespace-length"}
		source := "acm"
		out = append(out, protocol.Diagnostic{
			Range:    parsedoc.RangeFromNode(d.NamespaceNode),
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  fmt.Sprintf(`%s namespace "%s" is %d chars (max %d).`, d.Kind, d.Namespace, len(d.Namespace), maxLength),
		})
	}
	return out
}

// PolicyNamespaceLength is the exported rule instance.
var PolicyNamespaceLength Rule = policyNamespaceLength{}
