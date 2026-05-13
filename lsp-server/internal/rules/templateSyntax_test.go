package rules

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
	"github.com/acm-ls/lsp-server/internal/parsedoc"
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

func TestTemplateSyntax_LayeredOn_BrokenManagedSideCaught(t *testing.T) {
	// Managed-escape pair in source: helm renders to `{{ skipObject` (no
	// closing `}}` because the closer escape was deleted). Stage 1 helm
	// renders the opener escape to literal `{{`. Stage 2 (hub) doesn't
	// see hub markers, parses cleanly. Stage 2 executes with no hub
	// markers so output equals input. Stage 3 (managed) parses with
	// standard delims and reports unclosed `{{`.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{" }} skipObject
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) == 0 {
		t.Fatalf("expected managed-template parse error for unclosed runtime `{{`")
	}
	foundManaged := false
	for _, d := range diags {
		if strings.Contains(d.Message, "managed-template parse error") {
			foundManaged = true
		}
	}
	if !foundManaged {
		t.Errorf("expected at least one diagnostic with `managed-template parse error` prefix, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_BalancedManagedEscapeNoDiag(t *testing.T) {
	// Balanced managed-escape pair: helm renders to `{{ skipObject }}`,
	// stage 3 parses cleanly (skipObject is a managed-only function in
	// the catalog, registered as a stub).
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{" }} skipObject {{ "}}" }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) != 0 {
		t.Errorf("balanced managed-escape should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_FullThreeLayerStack(t *testing.T) {
	// All three layers in one block: helm `{{ if }}{{ end }}`, hub
	// escape with hub-side body, managed escape with managed-side body.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ if eq "x" "y" }}
    hub-side: '{{ "{{hub" }} fromConfigMap "ns" "n" "k" {{ "hub}}" }}'
    managed-side: '{{ "{{" }} skipObject {{ "}}" }}'
    {{ end }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) != 0 {
		t.Errorf("balanced full-stack template should produce no diagnostics, got: %+v", diags)
	}
}

func TestTemplateSyntax_LayeredOn_SubstringMapsToOriginalLine(t *testing.T) {
	// The unclosed `{{hub` is on the THIRD content line of the block.
	// After helm renders, the escape opener `{{ "{{hub" }}` collapses
	// to `{{hub` on the same line — so substring-matching the rendered
	// line back to the body should land on the right line in the
	// document (line index 4 — `spec:` at 0, `object-templates-raw:` at 1,
	// `data:` at 2, `key1: value` at 3, `bad: '{{ "{{hub" }} fn args' at 4`).
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    data:
      key1: value
      bad: '{{ "{{hub" }} fromConfigMap "ns" "n" "k"'
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxLayered()})
	if len(diags) == 0 {
		t.Fatalf("expected hub-template parse error")
	}
	if diags[0].Range.Start.Line != 4 {
		t.Errorf("expected diagnostic on line 4 (the line with the unclosed escape), got line %d", diags[0].Range.Start.Line)
	}
	if !strings.Contains(diags[0].Message, "rendered:") {
		t.Errorf("expected message to include rendered context, got: %q", diags[0].Message)
	}
}

func TestTemplateSyntax_VariableDefinedAtChartTop_NoFalsePositive(t *testing.T) {
	// Real-world pattern: `$policyNamespace` is defined at chart-top
	// (outside any block scalar) and referenced inside object-templates-
	// raw. We parse block scalars in isolation, so the parser doesn't
	// see the chart-top declaration. Phantom declarations get prepended
	// for any `$var` referenced but not declared inside the body.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `{{- $policyNamespace := .Values.policy_namespace -}}
spec:
  configurationPolicy:
    object-templates-raw: |
      data:
        ns: '{{ $policyNamespace }}'
        nested: '{{ "{{hub" }} fromConfigMap "{{ $policyNamespace }}" "name" "key" {{ "hub}}" }}'
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("chart-top variable used in block scalar should not trigger parse error, got: %+v", diags)
	}
}

func TestTemplateSyntax_VariableDeclaredInBody_NoPhantomCollision(t *testing.T) {
	// When a variable IS declared inside the body, no phantom is
	// prepended (otherwise `:=` would re-declare and parse-error).
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{- $local := "value" -}}
    key: '{{ $local }}'
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	if len(diags) != 0 {
		t.Errorf("locally-declared variable shouldn't trip phantom-redeclaration; got: %+v", diags)
	}
}

func TestTemplateSyntax_HelmActionRefInsideAcmEscape_NoFalseUndefined(t *testing.T) {
	// Real-chart pattern: `$policyNamespace` is declared inside a
	// managed-escape body (which is raw text from the helm parser's
	// view) and is referenced inside an embedded helm action also
	// inside that body. The textual `:=` doesn't declare anything in
	// helm scope, so the phantom-var pass must still inject a top-of-
	// template declaration to keep the parser from flagging the
	// helm-action `{{ $policyNamespace }}` as undefined.
	r := NewTemplateSyntax(miniTemplateResolver(), values.NewCache())
	text := `spec:
  object-templates-raw: |
    {{ "{{" }} $policyNamespace := "{{ "{{hub" }} (index .ManagedClusterLabels "{{ $.Values.x | default "autoshift.io/" }}policy-namespace") | default "{{ $policyNamespace }}" {{ "hub}}" }}" {{ "}}" }}
`
	diags := r.Run(Context{Text: text, Settings: enabledTemplateSyntaxSettings()})
	for _, d := range diags {
		if strings.Contains(d.Message, "undefined variable") {
			t.Errorf("variable declared by ACM escape pattern should not trip undefined-variable: %+v", d)
		}
	}
}

func TestBodyWithPhantomVars_DeclarationOnlyCountsInsideActions(t *testing.T) {
	// `$x := …` in raw text (outside `{{ … }}`) is not visible to the
	// helm parser, so the phantom-prepender must still emit a
	// declaration for `$x` even though it textually appears as a
	// declaration in the body.
	body := `text $x := "raw" more {{ $x }}`
	got := bodyWithPhantomVars(body)
	if !strings.Contains(got, `$x := ""`) {
		t.Errorf("expected phantom for $x even though `$x := ...` appears in raw text; got: %q", got)
	}
}

func TestBodyWithPhantomVars_OnlyPrependsUndeclared(t *testing.T) {
	body := `{{- $local := "x" -}}{{ $local }} {{ $external }} {{ $alsoExternal }}`
	got := bodyWithPhantomVars(body)
	if !strings.Contains(got, `$external := ""`) {
		t.Errorf("missing phantom for $external in: %q", got)
	}
	if !strings.Contains(got, `$alsoExternal := ""`) {
		t.Errorf("missing phantom for $alsoExternal in: %q", got)
	}
	// Only one occurrence of `$local := "x"` (the original), no phantom.
	if strings.Count(got, "$local := ") != 1 {
		t.Errorf(`local declaration should appear exactly once (no phantom), got: %q`, got)
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
	spans := parsedoc.FindObjectTemplatesRawBlocks(text)
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	body := text[spans[0].ContentStart:spans[0].ContentEnd]
	if !strings.Contains(body, "data:") {
		t.Errorf("body missing expected content: %q", body)
	}
	if strings.Contains(body, "other: not-included") {
		t.Errorf("body included a less-indented line that should have ended the block: %q", body)
	}
}
