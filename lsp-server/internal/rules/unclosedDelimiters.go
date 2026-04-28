package rules

import (
	"regexp"

	"github.com/acm-ls/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type unclosedDelimiters struct{}

func (unclosedDelimiters) ID() string { return "unclosed-delimiters" }

// Run flags two structural problems:
//
//  1. Go-template `{{` openers that don't have a matching `}}` closer (and
//     the symmetric stray `}}` with no preceding `{{`).
//  2. ACM hub-template `{{hub` / `hub}}` markers that aren't paired —
//     either an opener with no closer or a closer with no opener.
//
// Cheap to compute and runs alongside the heavier syntax check (the future
// text/template/parse rule), catching the most common authoring mistakes
// without depending on an AST.
func (unclosedDelimiters) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.unclosed-delimiters.enabled", true) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.unclosed-delimiters.severity", string(SeverityError)))

	sev := severity.ToLSP()
	code := protocol.IntegerOrString{Value: "unclosed-delimiters"}
	source := "acm"

	out := []protocol.Diagnostic{}
	out = append(out, scanGoTemplateDelims(ctx.Text, sev, code, source)...)
	out = append(out, scanHubMarkerPairs(ctx.Text, sev, code, source)...)
	return out
}

var UnclosedDelimiters Rule = unclosedDelimiters{}

// scanGoTemplateDelims walks the document and reports
//   - `{{` that never closes
//   - `}}` with no opener that consumed it
func scanGoTemplateDelims(text string, sev protocol.DiagnosticSeverity, code protocol.IntegerOrString, source string) []protocol.Diagnostic {
	out := []protocol.Diagnostic{}
	n := len(text)
	covered := make([]bool, n) // bytes covered by a balanced expression span

	i := 0
	unclosedReported := false
	for i+1 < n {
		if text[i] != '{' || text[i+1] != '{' {
			i++
			continue
		}
		end := findExprClose(text, i+2)
		if end < 0 {
			if !unclosedReported {
				out = append(out, protocol.Diagnostic{
					Range: protocol.Range{
						Start: offsetToPosition(text, i),
						End:   offsetToPosition(text, i+2),
					},
					Severity: &sev,
					Code:     &code,
					Source:   &source,
					Message:  `Unclosed go-template delimiter "{{" — no matching "}}".`,
				})
				unclosedReported = true
			}
			break
		}
		for k := i; k < end+2 && k < n; k++ {
			covered[k] = true
		}
		i = end + 2
	}

	for j := 0; j+1 < n; j++ {
		if text[j] == '}' && text[j+1] == '}' && !covered[j] {
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: offsetToPosition(text, j),
					End:   offsetToPosition(text, j+2),
				},
				Severity: &sev,
				Code:     &code,
				Source:   &source,
				Message:  `Stray closing delimiter "}}" — no matching "{{".`,
			})
			j++
		}
	}
	return out
}

var (
	hubOpenRE  = regexp.MustCompile(`\{\{-?\s*hub\b`)
	hubCloseRE = regexp.MustCompile(`-?\s*hub\s*-?\}\}`)
)

// scanHubMarkerPairs greedily pairs `{{hub` openers with the next `hub}}`
// closer that follows. Anything left over is an orphan. String-literal
// contents are skipped via context.FindHelmStringRanges so the inner `{{hub`
// of an escape-form pattern (`{{ "{{hub" }}`) doesn't show up as orphan.
func scanHubMarkerPairs(text string, sev protocol.DiagnosticSeverity, code protocol.IntegerOrString, source string) []protocol.Diagnostic {
	stringRanges := context.FindHelmStringRanges(text)
	insideString := func(off int) bool {
		for _, r := range stringRanges {
			if off >= r[0] && off < r[1] {
				return true
			}
		}
		return false
	}

	type marker struct {
		start, end int
	}
	opens := []marker{}
	for _, m := range hubOpenRE.FindAllStringIndex(text, -1) {
		if insideString(m[0]) {
			continue
		}
		opens = append(opens, marker{start: m[0], end: m[1]})
	}
	closes := []marker{}
	for _, m := range hubCloseRE.FindAllStringIndex(text, -1) {
		if insideString(m[0]) {
			continue
		}
		closes = append(closes, marker{start: m[0], end: m[1]})
	}

	usedOpens := make([]bool, len(opens))
	usedCloses := make([]bool, len(closes))
	ci := 0
	for oi, o := range opens {
		for ; ci < len(closes); ci++ {
			if closes[ci].start >= o.end {
				usedOpens[oi] = true
				usedCloses[ci] = true
				ci++
				break
			}
		}
	}

	out := []protocol.Diagnostic{}
	for oi, used := range usedOpens {
		if used {
			continue
		}
		out = append(out, protocol.Diagnostic{
			Range: protocol.Range{
				Start: offsetToPosition(text, opens[oi].start),
				End:   offsetToPosition(text, opens[oi].end),
			},
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  `Hub-template "{{hub" opener has no matching "hub}}" closer.`,
		})
	}
	for ci, used := range usedCloses {
		if used {
			continue
		}
		out = append(out, protocol.Diagnostic{
			Range: protocol.Range{
				Start: offsetToPosition(text, closes[ci].start),
				End:   offsetToPosition(text, closes[ci].end),
			},
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  `Hub-template "hub}}" closer has no matching "{{hub" opener.`,
		})
	}
	return out
}
