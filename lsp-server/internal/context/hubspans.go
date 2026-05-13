package context

import "regexp"

// HubSpan delimits the content portion of an ACM hub-template region.
// Direct: `{{hub ... hub}}` — content lives between contentStart and contentEnd.
// Escaped: double-split `{{ "{{hub" }} ... {{ "hub}}" }}` — content lives
// between the closing `}}` of the first escape and the opening `{{` of the
// second escape.
type HubSpan struct {
	ContentStart int
	ContentEnd   int
	Kind         SpanKind
}

type SpanKind int

const (
	SpanDirect SpanKind = iota
	SpanEscaped
)

var (
	directOpen  = regexp.MustCompile(`\{\{-?\s*hub\b`)
	directClose = regexp.MustCompile(`-?\s*hub\s*-?\}\}`)
	// Whole first escape expression: `{{ "{{hub" }}` or with trim
	// markers. Either kind of go-template string literal is accepted —
	// the standard `"…"` form and the backtick raw-string form `` `…` ``
	// — since Helm renders the action body to the literal's value
	// either way.
	escapedOpen = regexp.MustCompile(`\{\{-?\s*[\x60"]\{\{hub-?[\x60"]\s*-?\}\}`)
	// Whole second escape expression: `{{ "hub}}" }}`.
	escapedClose = regexp.MustCompile(`\{\{-?\s*[\x60"]-?hub\}\}[\x60"]\s*-?\}\}`)

	// Managed-cluster-side escape expressions, structurally identical to the
	// hub versions but without the "hub" keyword. Together they emit literal
	// `{{ ... }}` after Helm renders, which the managed-cluster ACM controller
	// processes at policy-evaluation time.
	managedEscapedOpen  = regexp.MustCompile(`\{\{-?\s*[\x60"]\{\{-?[\x60"]\s*-?\}\}`)
	managedEscapedClose = regexp.MustCompile(`\{\{-?\s*[\x60"]-?\}\}[\x60"]\s*-?\}\}`)
)

// FindHubSpans returns all hub-template content regions in the text.
func FindHubSpans(text string) []HubSpan {
	stringRanges := findHelmStringRanges(text)
	spans := []HubSpan{}

	for _, m := range directOpen.FindAllStringIndex(text, -1) {
		if isInsideAny(stringRanges, m[0]) {
			continue
		}
		closer := findDirectCloser(text, m[1])
		if closer == -1 {
			continue
		}
		spans = append(spans, HubSpan{ContentStart: m[1], ContentEnd: closer, Kind: SpanDirect})
	}

	for _, m := range escapedOpen.FindAllStringIndex(text, -1) {
		closer := findEscapedCloser(text, m[1])
		if closer == -1 {
			continue
		}
		spans = append(spans, HubSpan{ContentStart: m[1], ContentEnd: closer, Kind: SpanEscaped})
	}

	sortSpansByStart(spans)
	return dedupeNested(spans)
}

// IsInsideAnyHubSpan reports whether offset lies within any hub-span content.
func IsInsideAnyHubSpan(spans []HubSpan, offset int) bool {
	for _, s := range spans {
		if offset >= s.ContentStart && offset <= s.ContentEnd {
			return true
		}
	}
	return false
}

// ManagedSpan delimits the content portion of a managed-cluster escape pair
// `{{ "{{" }} ... {{ "}}" }}`. Unlike hub spans it has no direct/escaped
// distinction — managed templating only ever uses the escape form because
// a bare `{{` would be consumed by Helm at chart-render time.
type ManagedSpan struct {
	ContentStart int
	ContentEnd   int
}

// FindManagedSpans returns all managed-template content regions in the text.
// Spans nested inside hub spans are suppressed so the same content isn't
// processed twice.
func FindManagedSpans(text string) []ManagedSpan {
	hubSpans := FindHubSpans(text)
	out := []ManagedSpan{}
	for _, m := range managedEscapedOpen.FindAllStringIndex(text, -1) {
		closer := findManagedEscapedCloser(text, m[1])
		if closer == -1 {
			continue
		}
		span := ManagedSpan{ContentStart: m[1], ContentEnd: closer}
		if isManagedSpanInsideHub(hubSpans, span) {
			continue
		}
		out = append(out, span)
	}
	return out
}

// IsInsideAnyManagedSpan reports whether offset lies within any managed-span
// content range.
func IsInsideAnyManagedSpan(spans []ManagedSpan, offset int) bool {
	for _, s := range spans {
		if offset >= s.ContentStart && offset <= s.ContentEnd {
			return true
		}
	}
	return false
}

func findManagedEscapedCloser(text string, from int) int {
	loc := managedEscapedClose.FindStringIndex(text[from:])
	if loc == nil {
		return -1
	}
	return from + loc[0]
}

func isManagedSpanInsideHub(hubSpans []HubSpan, m ManagedSpan) bool {
	for _, h := range hubSpans {
		if m.ContentStart >= h.ContentStart && m.ContentEnd <= h.ContentEnd {
			return true
		}
	}
	return false
}

func findDirectCloser(text string, from int) int {
	loc := directClose.FindStringIndex(text[from:])
	if loc == nil {
		return -1
	}
	return from + loc[0]
}

func findEscapedCloser(text string, from int) int {
	loc := escapedClose.FindStringIndex(text[from:])
	if loc == nil {
		return -1
	}
	return from + loc[0]
}

func isInsideAny(ranges [][2]int, offset int) bool {
	for _, r := range ranges {
		if offset >= r[0] && offset < r[1] {
			return true
		}
	}
	return false
}

// FindHelmStringRanges is the exported wrapper for findHelmStringRanges so
// rule code outside this package can skip identifier matches that land inside
// string literals.
func FindHelmStringRanges(text string) [][2]int {
	return findHelmStringRanges(text)
}

// findHelmStringRanges scans Helm `{{ ... }}` expressions and returns the
// byte ranges of every string literal inside (`"..."` or `\`...\``). Ranges
// are inclusive of both quote characters.
func findHelmStringRanges(text string) [][2]int {
	ranges := [][2]int{}
	inExpr := false
	inString := false
	stringChar := byte(0)
	stringStart := -1
	i := 0
	for i < len(text) {
		c := text[i]
		if !inExpr {
			if c == '{' && i+1 < len(text) && text[i+1] == '{' {
				inExpr = true
				i += 2
				continue
			}
			i++
			continue
		}
		if !inString {
			if c == '"' || c == '`' {
				inString = true
				stringChar = c
				stringStart = i
				i++
				continue
			}
			// Skip `/* … */` go-template comments: `}}` inside the
			// comment body must not close the surrounding expression.
			if c == '/' && i+1 < len(text) && text[i+1] == '*' {
				i += 2
				for i+1 < len(text) && !(text[i] == '*' && text[i+1] == '/') {
					i++
				}
				if i+1 < len(text) {
					i += 2
				} else {
					i = len(text)
				}
				continue
			}
			if c == '}' && i+1 < len(text) && text[i+1] == '}' {
				inExpr = false
				i += 2
				continue
			}
			i++
			continue
		}
		// inside string
		if c == '\\' && stringChar == '"' && i+1 < len(text) {
			i += 2
			continue
		}
		if c == stringChar {
			ranges = append(ranges, [2]int{stringStart, i + 1})
			inString = false
			stringStart = -1
			i++
			continue
		}
		i++
	}
	return ranges
}

func sortSpansByStart(spans []HubSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j-1].ContentStart > spans[j].ContentStart; j-- {
			spans[j-1], spans[j] = spans[j], spans[j-1]
		}
	}
}

// dedupeNested suppresses direct spans whose content starts inside an
// escaped span's content range — those direct matches came from the inner
// {{hub literal of an escaped form, not real direct hub expressions.
func dedupeNested(spans []HubSpan) []HubSpan {
	kept := []HubSpan{}
	for _, s := range spans {
		nested := false
		for _, k := range kept {
			if k.Kind == SpanEscaped && s.ContentStart >= k.ContentStart && s.ContentEnd <= k.ContentEnd {
				nested = true
				break
			}
		}
		if !nested {
			kept = append(kept, s)
		}
	}
	return kept
}
