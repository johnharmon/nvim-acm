package rules

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/values"
)

func enabledTemplateSyntaxLayered() Settings {
	return Settings{
		"rules": map[string]any{
			"template-syntax": map[string]any{
				"enabled": true,
				"layered": true,
			},
		},
	}
}

func miniTemplateResolver() CatalogResolver {
	fn := func(name string) catalog.TemplateFunction {
		return catalog.TemplateFunction{Name: name}
	}
	return fakeResolver{resolved: catalog.Resolved{
		AcmVersion:       "test",
		HubFunctions:     []catalog.TemplateFunction{fn("fromConfigMap"), fn("fromSecret")},
		ManagedFunctions: []catalog.TemplateFunction{fn("skipObject")},
		HelmFunctions:    []catalog.TemplateFunction{fn("include"), fn("tpl")},
		SprigFunctions:   []catalog.TemplateFunction{fn("default"), fn("upper")},
		GoBuiltins: []catalog.TemplateFunction{
			fn("printf"), fn("index"), fn("len"), fn("eq"),
			fn("if"), fn("end"), fn("else"), fn("range"), fn("with"), fn("not"),
			fn("and"), fn("or"),
		},
	}}
}

func enabledTemplateSyntaxSettings() Settings {
	return Settings{
		"rules": map[string]any{
			"template-syntax": map[string]any{"enabled": true},
		},
	}
}

func TestTemplateSyntax_BalancedNoDiag(t *testing.T) {
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates-raw: |
    {{ if eq .Values.x "y" }}
    data:
      key: '{{hub fromConfigMap "" "cm" "k" hub}}'
      v: {{ printf "%s" .Values.foo }}
    {{ end }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("balanced template should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_MixedLayersOnSameLine(t *testing.T) {
	// Direct hub + managed escape + helm if/end on a single line. With
	// the stub FuncMap registered, the parser should see this as a
	// sequence of valid actions and accept it.
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `spec:
  object-templates-raw: |
    {{hub fromSecret "ns" "n" "k" hub}}-{{ "{{" }}skipObject{{ "}}" }}-{{ if .x }}foo{{ end }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("mixed-layer single line should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_MissingEnd(t *testing.T) {
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `spec:
  object-templates-raw: |
    {{ if .Values.x }}
    data:
      key: value
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) == 0 {
		t.Fatalf("expected a parse error for missing {{ end }}")
	}
	if !strings.Contains(diags[0].Message, "template parse error") {
		t.Errorf("unexpected message: %q", diags[0].Message)
	}
}

func TestTemplateSyntax_BadPipelineSyntax(t *testing.T) {
	// A leading pipe with no left-hand-side is a syntax error.
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `spec:
  object-templates-raw: |
    {{ | upper }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) == 0 {
		t.Fatalf("expected a parse error for bare pipe")
	}
}

func TestTemplateSyntax_PositionMapsToBlockLine(t *testing.T) {
	// The error happens on the third content line of the block scalar,
	// which is line 5 in the document (0-indexed: 4).
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `spec:
  object-templates-raw: |
    valid: '{{ printf "x" }}'
    also: 'fine'
    bad: '{{ if .x }}'
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) == 0 {
		t.Fatalf("expected a parse error")
	}
	// The unclosed `{{ if .x }}` is on the line after `also: 'fine'`,
	// which is line index 4 (0-indexed).
	if diags[0].Range.Start.Line != 4 {
		t.Errorf("diagnostic should land on document line 4, got line %d", diags[0].Range.Start.Line)
	}
}

func TestTemplateSyntax_DisabledByConfig(t *testing.T) {
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `spec:
  object-templates-raw: |
    {{ if .x }}
`
	settings := Settings{"rules": map[string]any{"template-syntax": map[string]any{"enabled": false}}}
	diags := r.Run(Context{Text: text, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("rule disabled but produced diagnostics: %+v", diags)
	}
}

func TestTemplateSyntax_NoBlockScalarNoDiag(t *testing.T) {
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `apiVersion: v1
kind: Policy
metadata:
  name: '{{ if .Values.x }}'
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("no object-templates-raw block scalar — rule should not fire, got: %+v", diags)
	}
}

func TestTemplateSyntax_MultipleBlocksIndependently(t *testing.T) {
	// Two block scalars in the same document. First is valid, second is
	// broken. Only the second should produce a diagnostic.
	r := NewTemplateSyntax(miniTemplateResolver(), nil)
	text := `---
spec:
  object-templates-raw: |
    {{ printf "ok" }}
---
spec:
  object-templates-raw: |
    {{ if .x }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic from second block, got %d: %+v", len(diags), diags)
	}
}

func TestTemplateSyntax_LayeredOff_StageTwoSkipped(t *testing.T) {
	// Without layered enabled, stage 2 is skipped — even broken hub
	// syntax in escape-form bodies stays silent.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{hub" }} fromConfigMap "ns" "name" "key" {{ "hub}}" }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("layered off — should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_BalancedStageTwoNoDiag(t *testing.T) {
	// Stage 2 parses the rendered output (`{{hub fromConfigMap … hub}}`)
	// with `{{hub`/`hub}}` delims. Balanced direct hub form parses fine.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{hub" }} fromConfigMap "ns" "name" "key" {{ "hub}}" }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) != 0 {
		t.Errorf("balanced stage-2 should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_BrokenHubSideCaught(t *testing.T) {
	// `{{ "{{hub" }} ... bareCloseHub` — stage 1 renders to
	// `{{hub fromConfigMap "ns" "name" "key"\n` (no `hub}}` closer at
	// stage 2's level). Stage 2's custom-delim parser will report an
	// unclosed `{{hub`.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{hub" }} fromConfigMap "ns" "name" "key"
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) == 0 {
		t.Fatalf("expected hub-template parse error for unclosed `{{hub`")
	}
	foundHub := false
	for _, d := range diags {
		if strings.Contains(d.Message, "hub-template parse error") {
			foundHub = true
		}
	}
	if !foundHub {
		t.Errorf("expected at least one diagnostic with `hub-template parse error` prefix, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_ChainedMissingValuesSkipsStageTwo(t *testing.T) {
	// `.Values.foo.bar.baz` would normally panic Execute with
	// nil-pointer on chained navigation. The stage-2 path swallows
	// execute errors silently and skips stage 2 — so the user sees
	// no spurious "hub parse error" for content that simply couldn't
	// be rendered. Phase B will surface execute errors as typed
	// diagnostics with proper handling.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ printf "%v" .Values.foo.bar.baz }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) != 0 {
		t.Errorf("execute failure should silently skip stage 2; got: %+v", diags)
	}
}

func TestFindObjectTemplatesRawBlocks_Indent(t *testing.T) {
	text := `spec:
  configurationPolicy:
    object-templates-raw: |
      data:
        key: value
      another: line
  other: not-included
`
	spans := findObjectTemplatesRawBlocks(text)
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	body := text[spans[0].contentStart:spans[0].contentEnd]
	if !strings.Contains(body, "data:") {
		t.Errorf("body missing expected content: %q", body)
	}
	if strings.Contains(body, "other: not-included") {
		t.Errorf("body included a less-indented line that should have ended the block: %q", body)
	}
}
