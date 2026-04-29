package rules

import (
	"strings"
	"testing"
	"text/template"

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

func TestRenderHelmStage_ChainedMissingValuesNoLongerPanics(t *testing.T) {
	// Phase B.1 regression: `.Values.foo.bar.baz` where none of the
	// intermediate keys exist used to nil-pointer during Execute. The
	// access-path pre-populator now ensures every traversed segment
	// has at least an empty-map placeholder, so Execute completes.
	body := `{{ printf "%v" .Values.foo.bar.baz }}
`
	out, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr != nil {
		t.Fatalf("unexpected parse error: %v", parseErr)
	}
	if execErr != nil {
		t.Fatalf("execute should succeed against pre-populated chain, got: %v", execErr)
	}
	// printf with an empty-string leaf renders to empty in this
	// stub setup. The exact output isn't asserted — what matters is
	// that Execute completed without panicking.
	_ = out
}

func TestRenderHelmStage_DeeplyNestedAccessPath(t *testing.T) {
	body := `{{ if .Values.policies.namespaces.allowList }}match{{ end }}
{{ printf "%v" .Values.policies.namespaces.allowList.first.name }}
`
	_, parseErr, execErr := renderHelmStage(body, nil, miniRenderResolved())
	if parseErr != nil {
		t.Fatalf("unexpected parse error: %v", parseErr)
	}
	if execErr != nil {
		t.Fatalf("deeply-nested access should not panic Execute, got: %v", execErr)
	}
}

func TestCollectAccessPaths_ReturnsAllFieldChains(t *testing.T) {
	body := `{{ .Values.x }}{{ .Release.Name }}{{ if .Values.foo.bar }}.{{ end }}`
	tmpl, err := template.New("t").
		Funcs(buildHelmStubFuncs(miniRenderResolved())).
		Option("missingkey=zero").
		Parse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	paths := collectAccessPaths(tmpl)
	want := map[string]bool{
		"Values.x":          false,
		"Release.Name":      false,
		"Values.foo.bar":    false,
	}
	for _, p := range paths {
		key := strings.Join(p, ".")
		if _, expected := want[key]; expected {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected to collect access path %q, didn't find it in %v", k, paths)
		}
	}
}

func TestEnsureAccessPaths_DoesntOverwriteExistingValues(t *testing.T) {
	ctx := map[string]any{
		"Values": map[string]any{
			"existing": "real-value",
		},
	}
	ensureAccessPaths(ctx, [][]string{
		{"Values", "existing"},   // already present — must not overwrite
		{"Values", "missing"},    // create
		{"Values", "deep", "nested", "leaf"}, // create chain
	})
	v := ctx["Values"].(map[string]any)
	if v["existing"] != "real-value" {
		t.Errorf("existing value was overwritten: got %v, want %q", v["existing"], "real-value")
	}
	if _, ok := v["missing"]; !ok {
		t.Errorf("missing path wasn't created: %v", v)
	}
	deep, ok := v["deep"].(map[string]any)
	if !ok {
		t.Fatalf("deep should be a map, got %T", v["deep"])
	}
	nested, ok := deep["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested should be a map, got %T", deep["nested"])
	}
	if _, ok := nested["leaf"]; !ok {
		t.Errorf("leaf wasn't populated: %v", nested)
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
