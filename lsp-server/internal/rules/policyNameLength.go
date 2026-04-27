package rules

import (
	"fmt"

	"github.com/autoshift/lsp-server/internal/parsedoc"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type policyNameLength struct{}

func (policyNameLength) ID() string { return "policy-name-length" }

func (policyNameLength) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.policy-name-length.enabled", true) {
		return nil
	}
	maxLength := GetInt(ctx.Settings, "rules.policy-name-length.maxLength", 63)
	severity := Severity(Get(ctx.Settings, "rules.policy-name-length.severity", string(SeverityWarning)))
	defaultKinds := []string{
		"Policy", "PlacementBinding", "PlacementRule", "Placement",
		"ConfigurationPolicy", "OperatorPolicy",
	}
	kinds := setOf(GetStringSlice(ctx.Settings, "rules.policy-name-length.kinds", defaultKinds))

	out := []protocol.Diagnostic{}
	for _, d := range ctx.Docs {
		if d.Kind == "" || d.Name == "" {
			continue
		}
		if !kinds[d.Kind] {
			continue
		}
		if len(d.Name) <= maxLength {
			continue
		}
		sev := severity.ToLSP()
		code := protocol.IntegerOrString{Value: "policy-name-length"}
		source := "autoshift"
		out = append(out, protocol.Diagnostic{
			Range:    parsedoc.RangeFromNode(d.NameNode),
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  fmt.Sprintf(`%s name "%s" is %d chars (max %d).`, d.Kind, d.Name, len(d.Name), maxLength),
		})
	}
	return out
}

// PolicyNameLength is the exported rule instance.
var PolicyNameLength Rule = policyNameLength{}

func setOf(vs []string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}
