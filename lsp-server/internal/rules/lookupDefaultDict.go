package rules

import (
	"regexp"
	"strings"

	"github.com/autoshift/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type lookupDefaultDict struct{}

func (lookupDefaultDict) ID() string { return "lookup-default-dict" }

// matches both bare `lookup "a" "b" "c" "d"` and parenthesized `lookup(`.
var lookupCallRE = regexp.MustCompile(`\blookup\s*\(|\blookup\s+"[^"]*"\s+"[^"]*"\s+"[^"]*"\s+"[^"]*"`)

func (lookupDefaultDict) Run(ctx Context) []protocol.Diagnostic {
	if !Get(ctx.Settings, "rules.lookup-default-dict.enabled", true) {
		return nil
	}
	severity := Severity(Get(ctx.Settings, "rules.lookup-default-dict.severity", string(SeverityWarning)))

	spans := context.FindHubSpans(ctx.Text)
	if len(spans) == 0 {
		return nil
	}

	out := []protocol.Diagnostic{}
	for _, span := range spans {
		body := ctx.Text[span.ContentStart:span.ContentEnd]
		for _, m := range lookupCallRE.FindAllStringIndex(body, -1) {
			callStart := m[0]
			callEnd := findCallEnd(body, callStart)
			if callEnd == -1 {
				continue
			}
			rest := strings.TrimLeft(body[callEnd:], " \t")
			if isFollowedByDefaultDict(rest) {
				continue
			}
			absStart := span.ContentStart + callStart
			absEnd := span.ContentStart + callEnd
			sev := severity.ToLSP()
			code := protocol.IntegerOrString{Value: "lookup-default-dict"}
			source := "autoshift"
			out = append(out, protocol.Diagnostic{
				Range: protocol.Range{
					Start: offsetToPosition(ctx.Text, absStart),
					End:   offsetToPosition(ctx.Text, absEnd),
				},
				Severity: &sev,
				Code:     &code,
				Source:   &source,
				Message:  `lookup result may be an empty map; pipe through "| default dict" before further map operations to avoid nil-access errors.`,
			})
		}
	}
	return out
}

var LookupDefaultDict Rule = lookupDefaultDict{}

func findCallEnd(body string, callStart int) int {
	const lookup = "lookup"
	pos := callStart + len(lookup)
	if pos >= len(body) {
		return -1
	}
	if body[pos] == '(' {
		depth := 0
		for i := pos; i < len(body); i++ {
			switch body[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		return -1
	}
	// space-form: skip whitespace + 4 string args
	seen := 0
	inString := false
	for i := pos; i < len(body); i++ {
		c := body[i]
		if inString {
			if c == '\\' && i+1 < len(body) {
				i++
				continue
			}
			if c == '"' {
				inString = false
				seen++
				if seen == 4 {
					return i + 1
				}
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '|' || c == '}' {
			return i
		}
	}
	return -1
}

func isFollowedByDefaultDict(rest string) bool {
	if !strings.HasPrefix(rest, "|") {
		return false
	}
	rest = strings.TrimLeft(rest[1:], " \t")
	return strings.HasPrefix(rest, "default") &&
		len(rest) > len("default") &&
		(rest[len("default")] == ' ' || rest[len("default")] == '\t') &&
		strings.HasPrefix(strings.TrimLeft(rest[len("default"):], " \t"), "dict")
}
