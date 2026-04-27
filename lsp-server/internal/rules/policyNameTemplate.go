package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/acm-ls/lsp-server/internal/context"
	"github.com/acm-ls/lsp-server/internal/parsedoc"
	"github.com/acm-ls/lsp-server/internal/values"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type Mode string

const (
	ModeStrict  Mode = "strict"
	ModeResolve Mode = "resolve"
	ModeBoth    Mode = "both"
)

var templateRE = regexp.MustCompile(`\{\{[^}]*\}\}`)

var defaultBroaderKinds = []string{
	"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding",
	"ServiceAccount", "ConfigMap", "Secret", "Namespace",
}

type policyNameTemplate struct {
	cache *values.Cache
}

// NewPolicyNameTemplate constructs the rule. The cache is shared with the
// completion path so values.yaml parses are amortized.
func NewPolicyNameTemplate(cache *values.Cache) Rule {
	return policyNameTemplate{cache: cache}
}

func (policyNameTemplate) ID() string { return "policy-name-template" }

func (r policyNameTemplate) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.policy-name-template.enabled", true) {
		return nil
	}
	maxLength := GetInt(ctx.Settings, "rules.policy-name-template.maxLength", 63)
	severity := Severity(Get(ctx.Settings, "rules.policy-name-template.severity", string(SeverityWarning)))
	overflowSeverity := Severity(Get(ctx.Settings, "rules.policy-name-template.overflowSeverity", string(SeverityError)))
	mode := Mode(Get(ctx.Settings, "rules.policy-name-template.mode", string(ModeStrict)))
	broaderKinds := GetStringSlice(ctx.Settings, "rules.policy-name-template.broaderKinds", defaultBroaderKinds)
	overrideKinds := GetStringSlice(ctx.Settings, "rules.policy-name-template.kinds", nil)

	docsHadAcm := context.IsAcmContextFromDocs(ctx.Docs)
	inAcm := context.IsAcmContextForFile(ctx.URI, docsHadAcm)

	activeKinds := map[string]bool{}
	if len(overrideKinds) > 0 {
		for _, k := range overrideKinds {
			activeKinds[k] = true
		}
	} else {
		for k := range context.ACMKinds {
			activeKinds[k] = true
		}
		if inAcm {
			for _, k := range broaderKinds {
				activeKinds[k] = true
			}
		}
	}

	resolver := r.buildResolver(ctx)

	out := []protocol.Diagnostic{}
	for _, d := range ctx.Docs {
		if d.Kind == "" || d.Name == "" || !activeKinds[d.Kind] {
			continue
		}
		match := templateRE.FindString(d.Name)
		if match == "" {
			continue
		}

		nodeRange := parsedoc.RangeFromNode(d.NameNode)
		constantLen := len(d.Name) - len(match)

		if mode != ModeStrict && resolver != nil {
			rendered, ok := values.RenderSimpleTemplate(d.Name, resolver)
			if ok {
				if len(rendered) <= maxLength {
					if mode == ModeResolve {
						continue
					}
				} else {
					sev := overflowSeverity.ToLSP()
					code := protocol.IntegerOrString{Value: "policy-name-template"}
					source := "acm"
					out = append(out, protocol.Diagnostic{
						Range:    nodeRange,
						Severity: &sev,
						Code:     &code,
						Source:   &source,
						Message:  fmt.Sprintf(`%s name "%s" renders to %d chars (max %d): "%s".`, d.Kind, d.Name, len(rendered), maxLength, rendered),
					})
					continue
				}
			}
		}

		var msg string
		budget := maxLength - constantLen
		switch {
		case constantLen > maxLength:
			msg = fmt.Sprintf(`%s name "%s" has %d constant chars before the template expression, already exceeding max %d.`, d.Kind, d.Name, constantLen, maxLength)
		case budget <= 0:
			msg = fmt.Sprintf(`%s name "%s" leaves 0 chars for the template expression output (max %d).`, d.Kind, d.Name, maxLength)
		default:
			msg = fmt.Sprintf(`%s name "%s" contains a template expression; rendered length = %d + expression output, must stay <= %d (budget: %d chars).`, d.Kind, d.Name, constantLen, maxLength, budget)
		}
		sev := severity.ToLSP()
		code := protocol.IntegerOrString{Value: "policy-name-template"}
		source := "acm"
		out = append(out, protocol.Diagnostic{
			Range:    nodeRange,
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  msg,
		})
	}
	return out
}

func (r policyNameTemplate) buildResolver(ctx Context) values.Resolver {
	if r.cache == nil {
		return nil
	}
	filePath := values.URIToPath(ctx.URI)
	if filePath == "" {
		return nil
	}
	chartRoot := values.FindChartRoot(filePath)
	if chartRoot == "" {
		return nil
	}
	root := r.cache.Get(chartRoot)
	if root == nil {
		return nil
	}
	return func(segments []string) *values.Node {
		return values.Navigate(root, segments)
	}
}

// Helper: avoid import cycle with strings.
var _ = strings.TrimSpace
