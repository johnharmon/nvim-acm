package rules

import (
	"fmt"
	"regexp"

	"github.com/autoshift/lsp-server/internal/parsedoc"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type policyNamePattern struct{}

func (policyNamePattern) ID() string { return "policy-name-pattern" }

func (policyNamePattern) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.policy-name-pattern.enabled", false) {
		return nil
	}
	patternSrc := Get(ctx.Settings, "rules.policy-name-pattern.pattern", "")
	if patternSrc == "" {
		return nil
	}
	pattern, err := regexp.Compile(patternSrc)
	if err != nil {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.policy-name-pattern.severity", string(SeverityInformation)))
	defaultKinds := []string{"Policy", "PlacementBinding", "PlacementRule", "Placement"}
	kinds := setOf(GetStringSlice(ctx.Settings, "rules.policy-name-pattern.kinds", defaultKinds))

	out := []protocol.Diagnostic{}
	for _, d := range ctx.Docs {
		if d.Kind == "" || d.Name == "" {
			continue
		}
		if !kinds[d.Kind] {
			continue
		}
		if pattern.MatchString(d.Name) {
			continue
		}
		sev := severity.ToLSP()
		code := protocol.IntegerOrString{Value: "policy-name-pattern"}
		source := "autoshift"
		out = append(out, protocol.Diagnostic{
			Range:    parsedoc.RangeFromNode(d.NameNode),
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  fmt.Sprintf(`%s name "%s" does not match pattern %s.`, d.Kind, d.Name, patternSrc),
		})
	}
	return out
}

var PolicyNamePattern Rule = policyNamePattern{}
