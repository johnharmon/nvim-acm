package rules

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
)

type fakeResolver struct {
	resolved catalog.Resolved
}

func (f fakeResolver) Resolve(_ string, _ catalog.UserExtras) catalog.Resolved {
	return f.resolved
}

func miniResolved() catalog.Resolved {
	fn := func(name string) catalog.TemplateFunction {
		return catalog.TemplateFunction{Name: name}
	}
	return catalog.Resolved{
		AcmVersion:       "test",
		HubFunctions:     []catalog.TemplateFunction{fn("fromConfigMap"), fn("fromSecret")},
		ManagedFunctions: []catalog.TemplateFunction{fn("lookup")},
		HelmFunctions:    []catalog.TemplateFunction{fn("include"), fn("tpl")},
		SprigFunctions:   []catalog.TemplateFunction{fn("upper"), fn("lower")},
		GoBuiltins:       []catalog.TemplateFunction{fn("printf"), fn("index"), fn("len")},
	}
}

func enabledSettings(extra ...map[string]any) Settings {
	rule := map[string]any{"enabled": true}
	for _, e := range extra {
		for k, v := range e {
			rule[k] = v
		}
	}
	return Settings{
		"rules": map[string]any{"unknown-function": rule},
	}
}

func TestUnknownFunction_DisabledByDefault(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `data: '{{hub bogusFn "x" hub}}'`
	diags := r.Run(Context{Text: text, Settings: Settings{}})
	if len(diags) != 0 {
		t.Errorf("rule must default off; got diagnostics: %+v", diags)
	}
}

func TestUnknownFunction_FlagsUnknownInHubSpan(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `data: '{{hub bogusFn "x" hub}}'`
	diags := r.Run(Context{Text: text, Settings: enabledSettings()})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `"bogusFn"`) {
		t.Errorf("diagnostic should name the unknown function, got: %q", diags[0].Message)
	}
}

func TestUnknownFunction_KnownInHubSpanNoDiag(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `data: '{{hub fromConfigMap "" "cm" "k" hub}}'`
	diags := r.Run(Context{Text: text, Settings: enabledSettings()})
	if len(diags) != 0 {
		t.Errorf("known function should not flag, got: %+v", diags)
	}
}

func TestUnknownFunction_HelmExpressionFlagged(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `key: {{ doesNotExist .Values.x }}`
	diags := r.Run(Context{Text: text, Settings: enabledSettings()})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `"doesNotExist"`) {
		t.Errorf("expected diagnostic on doesNotExist, got: %q", diags[0].Message)
	}
}

func TestUnknownFunction_SkipsKeywordsAndProperties(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `key: {{ if eq .Values.x "y" }}{{ printf "%s" .Values.foo }}{{ end }}`
	diags := r.Run(Context{Text: text, Settings: enabledSettings()})
	if len(diags) != 0 {
		t.Errorf("control keywords / properties shouldn't flag, got: %+v", diags)
	}
}

func TestUnknownFunction_AllowedFunctionsList(t *testing.T) {
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `key: {{ customHelper .Values.x }}`
	settings := enabledSettings(map[string]any{
		"allowedFunctions": []any{"customHelper"},
	})
	diags := r.Run(Context{Text: text, Settings: settings})
	if len(diags) != 0 {
		t.Errorf("allowedFunctions should silence; got: %+v", diags)
	}
}

func TestUnknownFunction_DedupesAcrossSpans(t *testing.T) {
	// Direct-form hub span: the OUTER expression `{{hub fromSecret ... hub}}`
	// is also an expression span, so the unknown-function scanner sees both
	// the hub-span content AND the wrapping expression. Same identifier
	// shouldn't emit twice.
	r := NewUnknownFunction(fakeResolver{resolved: miniResolved()})
	text := `data: '{{hub bogusFn "x" hub}}'`
	diags := r.Run(Context{Text: text, Settings: enabledSettings()})
	if len(diags) != 1 {
		t.Errorf("want exactly 1 diagnostic across hub+expression spans, got %d", len(diags))
	}
}
