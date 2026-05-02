package rules

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type unclosedParens struct{}

func (unclosedParens) ID() string { return "unclosed-parens" }

// Run walks every go-template expression span in the document and
// reports parens that don't pair within the span. Strings (`"…"` and
// `\`…\``) and `{{/* … */}}` comments are skipped so parens inside
// them don't count.
//
// Per-expression scope: a `(` opened in one `{{ … }}` action that's
// never closed in the same action is unmatched, regardless of whether
// some other action elsewhere has a stray `)`. Action bodies are
// independent in go-template syntax — function calls and pipelines
// don't span actions.
//
// Complements `template-syntax`: text/template/parse will eventually
// surface unmatched parens as a parse error, but earlier-in-edit
// feedback is more useful for live authoring.
func (unclosedParens) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.unclosed-parens.enabled", true) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.unclosed-parens.severity", string(SeverityWarning)))
	sev := severity.ToLSP()
	code := protocol.IntegerOrString{Value: "unclosed-parens"}
	source := "acm"

	out := []protocol.Diagnostic{}
	for _, sp := range findExpressionInners(ctx.Text) {
		for _, orphan := range scanOrphanParens(ctx.Text, sp.start, sp.end) {
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: offsetToPosition(ctx.Text, orphan.offset),
					End:   offsetToPosition(ctx.Text, orphan.offset+1),
				},
				Severity: &sev, Code: &code, Source: &source,
				Message: orphan.msg,
			})
		}
	}
	return out
}

// UnclosedParens is the exported rule instance.
var UnclosedParens Rule = unclosedParens{}

type parenOrphan struct {
	offset int
	msg    string
}

// scanOrphanParens tracks paren depth across `text[start:end)`. Returns
// one orphan record per unmatched `(` and one per stray `)`. String
// literals and `/* … */` comments are skipped so parens that are part
// of those don't affect the depth count. Offsets in the returned
// orphans are absolute document offsets.
func scanOrphanParens(text string, start, end int) []parenOrphan {
	if end > len(text) {
		end = len(text)
	}
	openStack := []int{}
	out := []parenOrphan{}
	i := start
	for i < end {
		c := text[i]
		if c == '"' || c == '`' {
			quote := c
			i++
			for i < end && text[i] != quote {
				if quote == '"' && text[i] == '\\' && i+1 < end {
					i += 2
					continue
				}
				i++
			}
			if i < end {
				i++
			}
			continue
		}
		if c == '/' && i+1 < end && text[i+1] == '*' {
			i += 2
			for i+1 < end && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			if i+1 < end {
				i += 2
			} else {
				i = end
			}
			continue
		}
		if c == '(' {
			openStack = append(openStack, i)
			i++
			continue
		}
		if c == ')' {
			if len(openStack) == 0 {
				out = append(out, parenOrphan{
					offset: i,
					msg:    `Stray ")" — no matching "(".`,
				})
			} else {
				openStack = openStack[:len(openStack)-1]
			}
			i++
			continue
		}
		i++
	}
	for _, off := range openStack {
		out = append(out, parenOrphan{
			offset: off,
			msg:    `Unclosed "(" — no matching ")".`,
		})
	}
	return out
}
