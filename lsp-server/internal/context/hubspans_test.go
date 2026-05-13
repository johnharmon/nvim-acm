package context

import "testing"

func TestFindHubSpans(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		expected int
	}{
		{"direct hub", `foo: '{{hub fromSecret "a" "b" "c" hub}}'`, 1},
		{"direct hub with trim markers", `foo: '{{hub- fromSecret "a" hub-}}'`, 1},
		{"escaped hub double-split", `{{ "{{hub" }} fromSecret "a" {{ "hub}}" }}`, 1},
		{"no hub", `foo: {{ .Values.bar }}`, 0},
		{"multiple direct", `{{hub a hub}} {{hub b hub}}`, 2},
		{"direct + escaped mixed", `{{hub a hub}} {{ "{{hub" }} b {{ "hub}}" }}`, 2},
		{"direct opener inside string literal is ignored", `foo: {{ "{{hub x hub}}" }}`, 0},
		{"escaped hub backtick raw-string form", "{{ `{{hub` }} fromSecret \"a\" {{ `hub}}` }}", 1},
		{"escaped hub mixed quote types open backtick close double", "{{ `{{hub` }} fromSecret \"a\" {{ \"hub}}\" }}", 1},
		{"nested hub escape pairs both kept", `{{ "{{hub" }} a {{ "{{hub" }} inner {{ "hub}}" }} b {{ "hub}}" }}`, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spans := FindHubSpans(c.text)
			if len(spans) != c.expected {
				t.Fatalf("got %d spans, want %d", len(spans), c.expected)
			}
		})
	}
}

func TestIsInsideAnyHubSpan(t *testing.T) {
	text := `{{hub TARGET hub}}`
	target := indexOfRune(text, 'T')
	spans := FindHubSpans(text)
	if !IsInsideAnyHubSpan(spans, target) {
		t.Fatalf("expected target offset %d to be inside a hub span", target)
	}

	other := `plain TARGET text`
	target = indexOfRune(other, 'T')
	spans = FindHubSpans(other)
	if IsInsideAnyHubSpan(spans, target) {
		t.Fatalf("offset should not be inside any span")
	}
}

func indexOfRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}
