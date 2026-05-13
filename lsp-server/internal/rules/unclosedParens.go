package rules

import (
	acmctx "github.com/acm-ls/lsp-server/internal/context"
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
// Three kinds of action body are scanned:
//   - Helm-level `{{ … }}` (this also covers direct hub form
//     `{{hub … hub}}` since the helm scanner walks past the `hub`
//     keyword to the trailing `}}`).
//   - Hub-escape body — the literal text between `{{ "{{hub" }}` and
//     `{{ "hub}}" }}`, which the hub controller parses as one action
//     after helm renders.
//   - Managed-escape body — the text between `{{ "{{" }}` and
//     `{{ "}}" }}`, which the managed-cluster ACM controller parses
//     after both helm and hub render.
//
// Inside escape bodies, nested helm `{{ … }}` actions are excluded
// from the scan because their parens are already counted by the
// helm-level pass — otherwise an unmatched `(` inside a nested helm
// action would be reported twice.
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
	emit := func(orphan parenOrphan) {
		out = append(out, protocol.Diagnostic{
			Range: protocol.Range{
				Start: offsetToPosition(ctx.Text, orphan.offset),
				End:   offsetToPosition(ctx.Text, orphan.offset+1),
			},
			Severity: &sev, Code: &code, Source: &source,
			Message: orphan.msg,
		})
	}

	helmSpans := findExpressionInners(ctx.Text)
	for _, sp := range helmSpans {
		for _, orphan := range scanOrphanParens(ctx.Text, sp.start, sp.end, nil) {
			emit(orphan)
		}
	}

	helmRanges := make([][2]int, 0, len(helmSpans))
	for _, sp := range helmSpans {
		// Use the outer `{{` … `}}` bounds, not the inner expression,
		// so the brace bytes themselves are also skipped during the
		// escape-body scan.
		outerStart := sp.start - 2
		if outerStart > 0 && ctx.Text[outerStart-1] == '-' {
			outerStart--
		}
		if outerStart < 0 {
			outerStart = 0
		}
		outerEnd := sp.end + 2
		if outerEnd > len(ctx.Text) {
			outerEnd = len(ctx.Text)
		}
		helmRanges = append(helmRanges, [2]int{outerStart, outerEnd})
	}

	for _, sp := range acmctx.FindHubSpans(ctx.Text) {
		if sp.Kind != acmctx.SpanEscaped {
			continue
		}
		for _, orphan := range scanOrphanParens(ctx.Text, sp.ContentStart, sp.ContentEnd, helmRanges) {
			emit(orphan)
		}
	}
	for _, sp := range acmctx.FindManagedSpans(ctx.Text) {
		for _, orphan := range scanOrphanParens(ctx.Text, sp.ContentStart, sp.ContentEnd, helmRanges) {
			emit(orphan)
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
//
// `skipRanges`, when non-nil, lists `[start, end)` byte ranges to
// pass over without counting any parens inside them. Used by the
// hub/managed escape-body scans to skip over nested helm `{{ … }}`
// actions whose parens are already covered by the helm-level pass.
func scanOrphanParens(text string, start, end int, skipRanges [][2]int) []parenOrphan {
	if end > len(text) {
		end = len(text)
	}
	openStack := []int{}
	out := []parenOrphan{}
	i := start
	for i < end {
		if skipped, jumpTo := jumpPastSkip(skipRanges, i); skipped {
			if jumpTo > end {
				i = end
			} else {
				i = jumpTo
			}
			continue
		}
		c := text[i]
		if c == '"' || c == '`' {
			quote := c
			i++
			for i < end && text[i] != quote {
				// Inside a hub/managed escape body the surrounding string
				// may contain an embedded helm `{{ … }}` action whose
				// rendered output becomes part of the string at chart
				// time. From the source-view we treat the action as
				// opaque: jump past it without letting its own string
				// literals (e.g. `"autoshift.io/"`) be mistaken for the
				// closing quote of the outer string.
				if skipped, jumpTo := jumpPastSkip(skipRanges, i); skipped {
					if jumpTo > end {
						i = end
					} else {
						i = jumpTo
					}
					continue
				}
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

// jumpPastSkip reports whether `offset` falls inside any skip range
// and, if so, returns the byte just past that range so the caller can
// resume scanning. Linear scan is fine — there are typically only a
// handful of helm spans inside any one escape body.
func jumpPastSkip(ranges [][2]int, offset int) (bool, int) {
	for _, r := range ranges {
		if offset >= r[0] && offset < r[1] {
			return true, r[1]
		}
	}
	return false, offset
}
