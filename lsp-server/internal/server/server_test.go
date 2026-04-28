package server

import (
	"testing"

	"github.com/acm-ls/lsp-server/internal/rules"
)

func TestNormalizeSettings_StripsAcmWrapper(t *testing.T) {
	in := map[string]any{
		"acm": map[string]any{
			"enabled": true,
			"acm":     map[string]any{"version": "2.15"},
			"rules": map[string]any{
				"policy-name-length": map[string]any{
					"enabled":   true,
					"maxLength": float64(120), // JSON unmarshal yields float64
				},
			},
		},
	}
	got := normalizeSettings(in)
	if v := rules.GetInt(got, "rules.policy-name-length.maxLength", -1); v != 120 {
		t.Errorf("rules.policy-name-length.maxLength: got %d, want 120", v)
	}
	if v := rules.Get[bool](got, "rules.policy-name-length.enabled", false); v != true {
		t.Errorf("rules.policy-name-length.enabled: got %v, want true", v)
	}
	if v := rules.Get[string](got, "acm.version", ""); v != "2.15" {
		t.Errorf("acm.version: got %q, want %q", v, "2.15")
	}
}

func TestNormalizeSettings_FlatShapeUntouched(t *testing.T) {
	// Some other client could send the unwrapped form directly.
	// normalizeSettings should return that map as-is.
	in := map[string]any{
		"rules": map[string]any{
			"policy-name-length": map[string]any{"maxLength": float64(50)},
		},
	}
	got := normalizeSettings(in)
	if v := rules.GetInt(got, "rules.policy-name-length.maxLength", -1); v != 50 {
		t.Errorf("rules.policy-name-length.maxLength: got %d, want 50", v)
	}
}

func TestNormalizeSettings_MissingAcmReturnsAsIs(t *testing.T) {
	in := map[string]any{
		"unrelated": "value",
	}
	got := normalizeSettings(in)
	if v := rules.Get[string](got, "unrelated", ""); v != "value" {
		t.Errorf("unrelated: got %q, want %q", v, "value")
	}
}

func TestNormalizeSettings_AcmIsNotMapReturnsAsIs(t *testing.T) {
	// If `acm` is somehow a string or other non-map, fall through to
	// using the raw map. (Defensive — should never happen in practice.)
	in := map[string]any{
		"acm":   "not-a-map",
		"rules": map[string]any{"x": map[string]any{"y": float64(1)}},
	}
	got := normalizeSettings(in)
	if v := rules.GetInt(got, "rules.x.y", -1); v != 1 {
		t.Errorf("rules.x.y: got %d, want 1", v)
	}
}
