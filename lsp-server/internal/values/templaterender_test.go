package values

import "testing"

func mkResolver(m map[string]string) Resolver {
	return func(segs []string) *Node {
		key := ""
		for i, s := range segs {
			if i > 0 {
				key += "."
			}
			key += s
		}
		v, ok := m[key]
		if !ok {
			return nil
		}
		return &Node{Type: TypeString, Example: v}
	}
}

func TestRenderSimpleTemplate(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		values   map[string]string
		expected string
		ok       bool
	}{
		{"plain value", `foo-{{ .Values.ns }}`, map[string]string{"ns": "prod"}, "foo-prod", true},
		{"root scope", `foo-{{ $.Values.ns }}`, map[string]string{"ns": "prod"}, "foo-prod", true},
		{"nested path", `x-{{ .Values.a.b.c }}`, map[string]string{"a.b.c": "deep"}, "x-deep", true},
		{"default applied when missing", `x-{{ .Values.missing | default "fallback" }}`, map[string]string{}, "x-fallback", true},
		{"default not applied when present", `x-{{ .Values.ns | default "fallback" }}`, map[string]string{"ns": "prod"}, "x-prod", true},
		{"quote wraps", `x-{{ .Values.ns | quote }}`, map[string]string{"ns": "prod"}, `x-"prod"`, true},
		{"trim markers", `x-{{- .Values.ns -}}`, map[string]string{"ns": "prod"}, "x-prod", true},
		{"unresolvable variable", `x-{{ $team }}`, map[string]string{}, "", false},
		{"unresolvable function", `x-{{ include "foo" . }}`, map[string]string{}, "", false},
		{"multiple resolvable", `{{ .Values.a }}-{{ .Values.b }}`, map[string]string{"a": "x", "b": "y"}, "x-y", true},
		{"multiple one unresolvable", `{{ .Values.a }}-{{ $b }}`, map[string]string{"a": "x"}, "", false},
		{"no expressions passes through", `plain-text`, map[string]string{}, "plain-text", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RenderSimpleTemplate(c.tmpl, mkResolver(c.values))
			if ok != c.ok {
				t.Fatalf("ok=%v want %v (got=%q)", ok, c.ok, got)
			}
			if c.ok && got != c.expected {
				t.Fatalf("got %q, want %q", got, c.expected)
			}
		})
	}
}
