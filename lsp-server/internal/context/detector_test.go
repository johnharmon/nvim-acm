package context

import (
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

func TestDetectLayerAt(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		expectLayer Layer
		expectRaw   bool
	}{
		{"bare helm expression", `foo: {{ |CURSOR| }}`, LayerHelm, false},
		{"helm with value ref", `foo: {{ .Values.|CURSOR|bar }}`, LayerHelm, false},
		{"outside any mustache", `foo: bar|CURSOR|`, LayerNone, false},
		{"bare hub expression", `foo: '{{hub fromSecret "ns" "n" "k"|CURSOR| hub}}'`, LayerHub, false},
		{"hub with trim markers", `foo: '{{hub- fromSecret "ns" "n" "|CURSOR|k" -hub}}'`, LayerHub, false},
		{
			"helm-escaped hub inside regular helm expr",
			`foo: {{ "{{hub fromSecret \"n\" \"s\" \"k\"|CURSOR| hub}}" }}`,
			LayerHub, false,
		},
		{
			"managed template inside object-templates-raw escaped via helm",
			"spec:\n  object-templates-raw: |\n    {{- range (lookup \"v1\" \"ConfigMap\" \"default\" \"\").items }}\n    - objectDefinition:\n        data:\n          foo: '{{ \"{{ fromSecret \\\"ns\\\" \\\"n\\\" \\\"|CURSOR|k\\\" }}\" }}'\n    {{- end }}\n",
			LayerManaged, true,
		},
		{
			"plain text inside object-templates-raw flags managed",
			"spec:\n  object-templates-raw: |\n    - complianceType: musthave\n      objectDefinition:\n        metadata:\n          name: bar|CURSOR|\n",
			LayerManaged, true,
		},
		{"cursor in values outside raw", "spec:\n  replicas: {{ .Values.rep|CURSOR|licas }}\n", LayerHelm, false},
		{
			"hub inside object-templates-raw via helm escape",
			"spec:\n  object-templates-raw: |\n    - objectDefinition:\n        data:\n          x: '{{ \"{{hub fromSecret \\\"ns\\\" \\\"n\\\" \\\"|CURSOR|k\\\" hub}}\" }}'\n",
			LayerHub, true,
		},
		{
			// Regression: cursor in the LITERAL TEXT between two double-split
			// escape Helm expressions used to be misdetected as managed
			// (because we fell through to insideRaw without checking hub spans).
			// The text between `{{ "{{hub" }}` and `{{ "hub}}" }}` is hub
			// content even though it's not inside a Helm `{{...}}`.
			"plain text between double-split escape exprs is hub",
			"spec:\n  object-templates-raw: |\n    {{ \"{{hub\" }} index .ManagedCluster|CURSOR|Labels \"foo\" {{ \"hub}}\" }}\n",
			LayerHub, true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, offset := splitCursor(c.input)
			got := DetectLayerAt(text, offset)
			if got.Layer != c.expectLayer {
				t.Errorf("layer: got %s want %s", got.Layer, c.expectLayer)
			}
			if got.InsideObjectTemplatesRaw != c.expectRaw {
				t.Errorf("raw: got %v want %v", got.InsideObjectTemplatesRaw, c.expectRaw)
			}
		})
	}
}
