package rules

import (
	"regexp"
	"sort"

	"github.com/acm-ls/lsp-server/internal/context"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

type unclosedDelimiters struct{}

func (unclosedDelimiters) ID() string { return "unclosed-delimiters" }

// Run flags structural problems at four conceptual layers:
//
//  1. Go-template `{{` openers without a matching `}}` closer (and the
//     symmetric stray `}}`).
//  2. Direct ACM hub markers `{{hub` / `hub}}` that aren't paired.
//  3. Hub-escape pairs `{{ "{{hub" }}` / `{{ "hub}}" }}` — the form
//     helm renders into runtime `{{hub`/`hub}}` markers — that aren't
//     paired.
//  4. Managed-escape pairs `{{ "{{" }}` / `{{ "}}" }}` — the form
//     helm renders into the runtime `{{`/`}}` the managed-cluster ACM
//     controller evaluates — that aren't paired.
//
// Each layer is its own state-machine pass over the document. Cheap to
// compute and runs alongside the heavier syntax check (the future
// text/template/parse rule), catching the most common authoring
// mistakes without depending on an AST.
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
	out = append(out, scanMarkerPairs(ctx.Text, directHubPair, sev, code, source)...)
	out = append(out, scanMarkerPairs(ctx.Text, hubEscapePair, sev, code, source)...)
	out = append(out, scanMarkerPairs(ctx.Text, managedEscapePair, sev, code, source)...)
	return out
}

var UnclosedDelimiters Rule = unclosedDelimiters{}

// scanGoTemplateDelims walks the document with a small state machine and
// reports every unbalanced `{{` and stray `}}`. State:
//
//	OUTSIDE  no open expression — `{{` opens, `}}` is stray
//	INSIDE   an expression is open — `}}` closes, another `{{` means
//	         the previous opener was never closed; strings (`"…"` and
//	         `\`…\``) are skipped while INSIDE so `}}` literals don't
//	         falsely close.
//
// Stack-style pairing rather than greedy "next `}}` after this `{{`",
// because greedy pairing silently steals the close of a later balanced
// expression to satisfy an earlier unclosed one — hiding imbalance
// during live editing.
func scanGoTemplateDelims(text string, sev protocol.DiagnosticSeverity, code protocol.IntegerOrString, source string) []protocol.Diagnostic {
	out := []protocol.Diagnostic{}
	emit := func(start, end int, msg string) {
		out = append(out, protocol.Diagnostic{
			Range: protocol.Range{
				Start: offsetToPosition(text, start),
				End:   offsetToPosition(text, end),
			},
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  msg,
		})
	}

	n := len(text)
	openerOffset := -1 // -1 means OUTSIDE
	i := 0
	for i < n {
		c := text[i]
		if openerOffset >= 0 && (c == '"' || c == '`') {
			quote := c
			i++
			for i < n && text[i] != quote {
				if quote == '"' && text[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i < n {
				i++
			}
			continue
		}
		// Inside an expression, `/* … */` is a go-template comment.
		// Comments are opaque content — `{{` and `}}` literals inside
		// must not affect the open/close state machine. Skip the
		// whole comment to the `*/` terminator (or EOF if unterminated).
		if openerOffset >= 0 && i+1 < n && c == '/' && text[i+1] == '*' {
			i += 2
			for i+1 < n && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2 // step over `*/`
			} else {
				i = n
			}
			continue
		}
		if i+1 < n && c == '{' && text[i+1] == '{' {
			if openerOffset >= 0 {
				emit(openerOffset, openerOffset+2, `Unclosed go-template delimiter "{{" — no matching "}}".`)
			}
			openerOffset = i
			i += 2
			continue
		}
		if i+1 < n && c == '}' && text[i+1] == '}' {
			if openerOffset < 0 {
				emit(i, i+2, `Stray closing delimiter "}}" — no matching "{{".`)
			} else {
				openerOffset = -1
			}
			i += 2
			continue
		}
		i++
	}
	if openerOffset >= 0 {
		emit(openerOffset, openerOffset+2, `Unclosed go-template delimiter "{{" — no matching "}}".`)
	}
	return out
}

// markerPair describes a layer of paired delimiters.
type markerPair struct {
	openRE         *regexp.Regexp
	closeRE        *regexp.Regexp
	openOrphanMsg  string
	closeOrphanMsg string
	// skipInString controls whether matches that fall inside a helm-string
	// literal are ignored. Direct hub markers (`{{hub`/`hub}}`) need this
	// because they false-match inside escape-form string literals; escape-
	// form regexes (`{{ "{{" }}` etc.) own the surrounding `{{`/`}}` and
	// the `"…"` is part of the matched structure, so the start of the
	// match is never inside another string.
	skipInString bool
}

var (
	directHubPair = markerPair{
		openRE:         regexp.MustCompile(`\{\{-?\s*hub\b`),
		closeRE:        regexp.MustCompile(`-?\s*hub\s*-?\}\}`),
		openOrphanMsg:  `Hub-template "{{hub" opener has no matching "hub}}" closer.`,
		closeOrphanMsg: `Hub-template "hub}}" closer has no matching "{{hub" opener.`,
		skipInString:   true,
	}
	hubEscapePair = markerPair{
		openRE:         regexp.MustCompile(`\{\{-?\s*"\{\{hub-?"\s*-?\}\}`),
		closeRE:        regexp.MustCompile(`\{\{-?\s*"-?hub\}\}"\s*-?\}\}`),
		openOrphanMsg:  `Hub-escape opener {{ "{{hub" }} has no matching {{ "hub}}" }} closer.`,
		closeOrphanMsg: `Hub-escape closer {{ "hub}}" }} has no matching {{ "{{hub" }} opener.`,
		skipInString:   false,
	}
	managedEscapePair = markerPair{
		openRE:         regexp.MustCompile(`\{\{-?\s*"\{\{-?"\s*-?\}\}`),
		closeRE:        regexp.MustCompile(`\{\{-?\s*"-?\}\}"\s*-?\}\}`),
		openOrphanMsg:  `Managed-escape opener {{ "{{" }} has no matching {{ "}}" }} closer.`,
		closeOrphanMsg: `Managed-escape closer {{ "}}" }} has no matching {{ "{{" }} opener.`,
		skipInString:   false,
	}
)

// scanMarkerPairs walks open/close matches in document order and runs the
// same state machine as scanGoTemplateDelims. An unclosed opener still in
// the "open" slot when a new opener arrives is reported as orphan; a
// closer with nothing open is reported as stray.
func scanMarkerPairs(text string, p markerPair, sev protocol.DiagnosticSeverity, code protocol.IntegerOrString, source string) []protocol.Diagnostic {
	type marker struct {
		isOpen     bool
		start, end int
	}

	var stringRanges [][2]int
	if p.skipInString {
		stringRanges = context.FindHelmStringRanges(text)
	}
	insideString := func(off int) bool {
		for _, r := range stringRanges {
			if off >= r[0] && off < r[1] {
				return true
			}
		}
		return false
	}

	markers := []marker{}
	for _, m := range p.openRE.FindAllStringIndex(text, -1) {
		if p.skipInString && insideString(m[0]) {
			continue
		}
		markers = append(markers, marker{isOpen: true, start: m[0], end: m[1]})
	}
	for _, m := range p.closeRE.FindAllStringIndex(text, -1) {
		if p.skipInString && insideString(m[0]) {
			continue
		}
		markers = append(markers, marker{isOpen: false, start: m[0], end: m[1]})
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].start < markers[j].start })

	out := []protocol.Diagnostic{}
	emit := func(start, end int, msg string) {
		out = append(out, protocol.Diagnostic{
			Range: protocol.Range{
				Start: offsetToPosition(text, start),
				End:   offsetToPosition(text, end),
			},
			Severity: &sev,
			Code:     &code,
			Source:   &source,
			Message:  msg,
		})
	}

	openIdx := -1
	for i, m := range markers {
		if m.isOpen {
			if openIdx >= 0 {
				prev := markers[openIdx]
				emit(prev.start, prev.end, p.openOrphanMsg)
			}
			openIdx = i
			continue
		}
		if openIdx < 0 {
			emit(m.start, m.end, p.closeOrphanMsg)
			continue
		}
		openIdx = -1
	}
	if openIdx >= 0 {
		prev := markers[openIdx]
		emit(prev.start, prev.end, p.openOrphanMsg)
	}
	return out
}
