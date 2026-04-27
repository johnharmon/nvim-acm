package values

import (
	"reflect"
	"strings"
	"testing"
)

func splitCursor(input string) (text string, offset int) {
	const marker = "|CURSOR|"
	offset = strings.Index(input, marker)
	if offset == -1 {
		panic("expected |CURSOR| marker")
	}
	return strings.Replace(input, marker, "", 1), offset
}

func TestParseValuesPathBeforeCursor(t *testing.T) {
	cases := []struct {
		input    string
		expected *ValuesPath
	}{
		{`{{ .Values.|CURSOR| }}`, &ValuesPath{Segments: []string{}, Prefix: ""}},
		{`{{ .Values.|CURSOR|foo }}`, &ValuesPath{Segments: []string{}, Prefix: ""}},
		{`{{ .Values.foo|CURSOR| }}`, &ValuesPath{Segments: []string{}, Prefix: "foo"}},
		{`{{ .Values.foo.|CURSOR| }}`, &ValuesPath{Segments: []string{"foo"}, Prefix: ""}},
		{`{{ .Values.foo.bar|CURSOR| }}`, &ValuesPath{Segments: []string{"foo"}, Prefix: "bar"}},
		{`{{ .Values.foo.bar.baz|CURSOR| }}`, &ValuesPath{Segments: []string{"foo", "bar"}, Prefix: "baz"}},
		{`{{ $.Values.foo.|CURSOR| }}`, &ValuesPath{Segments: []string{"foo"}, Prefix: ""}},
		{`{{ .Other.foo|CURSOR| }}`, nil},
		{`{{ .Values|CURSOR| }}`, nil},
		{`plaintext|CURSOR|`, nil},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			text, offset := splitCursor(c.input)
			got, ok := ParseValuesPathBeforeCursor(text, offset)
			if c.expected == nil {
				if ok {
					t.Fatalf("expected miss, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected hit, got miss")
			}
			if !sliceEq(got.Segments, c.expected.Segments) || got.Prefix != c.expected.Prefix {
				t.Fatalf("got segments=%v prefix=%q; want segments=%v prefix=%q",
					got.Segments, got.Prefix, c.expected.Segments, c.expected.Prefix)
			}
		})
	}
}

func sliceEq(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
