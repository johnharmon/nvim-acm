package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/acm-ls/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type hubForbiddenFunctions struct{}

func (hubForbiddenFunctions) ID() string { return "hub-forbidden-functions" }

func (hubForbiddenFunctions) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.hub-forbidden-functions.enabled", true) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.hub-forbidden-functions.severity", string(SeverityError)))
	defaultFns := []string{"trimPrefix", "trimSuffix"}
	forbidden := GetStringSlice(ctx.Settings, "rules.hub-forbidden-functions.functions", defaultFns)
	if len(forbidden) == 0 {
		return nil
	}

	spans := context.FindHubSpans(ctx.Text)
	if len(spans) == 0 {
		return nil
	}

	parts := make([]string, len(forbidden))
	for i, name := range forbidden {
		parts[i] = regexp.QuoteMeta(name)
	}
	re := regexp.MustCompile(`\b(` + strings.Join(parts, "|") + `)\b`)

	out := []protocol.Diagnostic{}
	for _, span := range spans {
		body := ctx.Text[span.ContentStart:span.ContentEnd]
		for _, m := range re.FindAllStringIndex(body, -1) {
			absStart := span.ContentStart + m[0]
			absEnd := span.ContentStart + m[1]
			startPos := offsetToPosition(ctx.Text, absStart)
			endPos := offsetToPosition(ctx.Text, absEnd)
			fnName := body[m[0]:m[1]]
			sev := severity.ToLSP()
			code := protocol.IntegerOrString{Value: "hub-forbidden-functions"}
			source := "acm"
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: startPos,
					End:   endPos,
				},
				Severity: &sev,
				Code:     &code,
				Source:   &source,
				Message:  fmt.Sprintf(`Function %q is not available in ACM hub templates. Use replace or a different pipeline.`, fnName),
			})
		}
	}
	return out
}

var HubForbiddenFunctions Rule = hubForbiddenFunctions{}

// offsetToPosition converts a byte offset into a 0-indexed LSP Position by
// counting newlines. Acceptable for the document sizes we deal with.
func offsetToPosition(text string, offset int) protocol.Position {
	line := uint32(0)
	col := uint32(0)
	if offset > len(text) {
		offset = len(text)
	}
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return protocol.Position{Line: line, Character: col}
}
