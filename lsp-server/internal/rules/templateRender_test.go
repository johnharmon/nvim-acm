package rules

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/values"
)

func miniRenderResolved() catalog.Resolved {
	fn := func(name string) catalog.TemplateFunction {
		return catalog.TemplateFunction{Name: name}
	}
	val := func(name, ty string) catalog.ExportedValue {
		return catalog.ExportedValue{Name: name, Type: ty}
	}
	return catalog.Resolved{
		AcmVersion:    "test",
		HelmFunctions: []catalog.TemplateFunction{fn("include"), fn("tpl")},
		HubFunctions:  []catalog.TemplateFunction{fn("fromConfigMap"), fn("fromSecret")},
		ManagedFunctions: []catalog.TemplateFunction{
			fn("skipObject"),
		},
		SprigFunctions: []catalog.TemplateFunction{fn("default"), fn("upper")},
		GoBuiltins: []catalog.TemplateFunction{
			fn("printf"), fn("index"), fn("len"), fn("eq"),
		},
		HelmContextValues: []catalog.ExportedValue{
			val(".Release.Name", "string"),
			val(".Release.Namespace", "string"),
			val(".Release.IsInstall", "bool"),
			val(".Chart.Name", "string"),
			val(".Chart.Version", "string"),
		},
	}
}

func TestRenderHelmStage_BalancedRenders(t *testing.T) {
	body := `key: '{{ printf "%s" "static" }}'
ns:  '{{ .Release.Namespace }}'
`
	out, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr != nil {
		t.Fatalf("unexpected parse error: %v", parseErr)
	}
	if execErr != nil {
		t.Fatalf("unexpected execute error: %v", execErr)
	}
	if !strings.Contains(out, "key: ''") {
		t.Errorf("expected printf stub to produce empty output. got: %q", out)
	}
	if !strings.Contains(out, "ns:  ''") {
		t.Errorf("expected .Release.Namespace sentinel to be empty string. got: %q", out)
	}
}

func TestRenderHelmStage_EscapeFormCollapses(t *testing.T) {
	// The hub-escape opener `{{ "{{hub" }}` is a string-literal action
	// whose Execute output is the literal `{{hub`. After stage 1 render,
	// the post-helm text contains `{{hub …` — exactly what stage 2's
	// custom-delim parser will need.
	body := `{{ "{{hub" }} fromConfigMap "ns" "name" "key" {{ "hub}}" }}
`
	out, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr != nil {
		t.Fatalf("unexpected parse error: %v", parseErr)
	}
	if execErr != nil {
		t.Fatalf("unexpected execute error: %v", execErr)
	}
	if !strings.Contains(out, "{{hub fromConfigMap") {
		t.Errorf("escape form should collapse to direct hub form. got: %q", out)
	}
	if !strings.Contains(out, "hub}}") {
		t.Errorf("escape closer should collapse to `hub}}` literal. got: %q", out)
	}
}

func TestRenderHelmStage_ManagedEscapeCollapses(t *testing.T) {
	body := `data: '{{ "{{" }} skipObject {{ "}}" }}'
`
	out, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr != nil {
		t.Fatalf("unexpected parse error: %v", parseErr)
	}
	if execErr != nil {
		t.Fatalf("unexpected execute error: %v", execErr)
	}
	if !strings.Contains(out, "{{ skipObject }}") {
		t.Errorf("managed escape should collapse to runtime `{{ skipObject }}`. got: %q", out)
	}
}

func TestRenderHelmStage_ParseErrorIsReturned(t *testing.T) {
	body := `{{ if .Values.x }}
no end here
`
	_, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr == nil {
		t.Errorf("expected parse error for missing {{ end }}")
	}
	if execErr != nil {
		t.Errorf("execute error should be nil when parse failed; got: %v", execErr)
	}
}

func TestBuildHelmDataContext_PrePopulatesRelease(t *testing.T) {
	resolved := miniRenderResolved()
	ctx := buildHelmDataContext(nil, resolved)
	rel, ok := ctx["Release"].(map[string]any)
	if !ok {
		t.Fatalf(".Release should be a map, got %T", ctx["Release"])
	}
	if _, ok := rel["Name"].(string); !ok {
		t.Errorf(".Release.Name should be a string sentinel, got %T", rel["Name"])
	}
	if _, ok := rel["IsInstall"].(bool); !ok {
		t.Errorf(".Release.IsInstall should be a bool sentinel, got %T", rel["IsInstall"])
	}
	for _, name := range []string{"Chart", "Files", "Capabilities", "Template"} {
		if _, ok := ctx[name]; !ok {
			t.Errorf(".%s should always be present in the data context", name)
		}
	}
}

func TestValuesNodeToAny_ConvertsTree(t *testing.T) {
	root := &values.Node{
		Type: values.TypeMap,
		Children: map[string]*values.Node{
			"foo": {Type: values.TypeString, Example: "bar"},
			"n":   {Type: values.TypeNumber, Example: "42"},
			"b":   {Type: values.TypeBoolean, Example: "true"},
			"nested": {
				Type: values.TypeMap,
				Children: map[string]*values.Node{
					"x": {Type: values.TypeString, Example: "y"},
				},
			},
		},
	}
	got := valuesNodeToAny(root).(map[string]any)
	if got["foo"] != "bar" {
		t.Errorf("foo: got %v, want bar", got["foo"])
	}
	if got["n"] != int64(42) {
		t.Errorf("n: got %v (%T), want 42 (int64)", got["n"], got["n"])
	}
	if got["b"] != true {
		t.Errorf("b: got %v, want true", got["b"])
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested should be a map")
	}
	if nested["x"] != "y" {
		t.Errorf("nested.x: got %v, want y", nested["x"])
	}
}

func TestSentinelForType(t *testing.T) {
	cases := []struct {
		ty   string
		want any
	}{
		{"string", ""},
		{"int", 0},
		{"bool", false},
		{"list", []any{}},
		{"map", map[string]any{}},
		{"unknown-type-name", ""}, // safe fallback to string
	}
	for _, c := range cases {
		got := sentinelForType(c.ty)
		switch want := c.want.(type) {
		case []any:
			gotSlice, ok := got.([]any)
			if !ok || len(gotSlice) != 0 {
				t.Errorf("sentinelForType(%q) = %v, want %v", c.ty, got, want)
			}
		case map[string]any:
			gotMap, ok := got.(map[string]any)
			if !ok || len(gotMap) != 0 {
				t.Errorf("sentinelForType(%q) = %v, want %v", c.ty, got, want)
			}
		default:
			if got != want {
				t.Errorf("sentinelForType(%q) = %v, want %v", c.ty, got, want)
			}
		}
	}
}
