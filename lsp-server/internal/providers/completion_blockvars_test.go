package providers

import (
	"strings"
	"testing"

	"github.com/acm-ls/lsp-server/internal/catalog"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// findOffset returns the byte offset of the first occurrence of marker
// in text. Used by tests to point Position at a specific spot in a
// fixture without manual line/col counting.
func findOffset(text, marker string) int {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return 0
	}
	return idx + len(marker)
}

func positionFromOffset(text string, offset int) protocol.Position {
	line := uint32(0)
	col := uint32(0)
	for i := 0; i < offset && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return protocol.Position{Line: line, Character: col}
}

func TestBlockScopedVarItems_PicksUpDeclarationsInBlock(t *testing.T) {
	text := `spec:
  configurationPolicy:
    object-templates-raw: |
      {{- $obsConfig := index (lookup "ns" "n") "k" -}}
      {{- $rwList := index $obsConfig "additionalRemoteWrites" -}}
      key: '{{ $`
	off := len(text)
	got := blockScopedVarItems(text, off)
	if len(got) < 2 {
		t.Fatalf("want at least 2 var items, got %d: %+v", len(got), got)
	}
	names := map[string]bool{}
	for _, item := range got {
		names[item.Label] = true
	}
	for _, want := range []string{"$obsConfig", "$rwList"} {
		if !names[want] {
			t.Errorf("missing %q in completion items: %+v", want, names)
		}
	}
}

func TestBlockScopedVarItems_OutsideBlockReturnsNone(t *testing.T) {
	text := `metadata:
  name: foo
spec:
  bar: '{{ $`
	off := len(text)
	got := blockScopedVarItems(text, off)
	if len(got) != 0 {
		t.Errorf("cursor outside any object-templates-raw block should return zero items, got: %+v", got)
	}
}

func TestBlockScopedVarItems_DedupesByName(t *testing.T) {
	// Same variable assigned twice (e.g. inside a `{{ range }}` loop)
	// should appear once.
	text := `spec:
  object-templates-raw: |
    {{- $x := "a" -}}
    {{- range .y -}}
    {{- $x := . -}}
    {{- end -}}
    `
	off := len(text)
	got := blockScopedVarItems(text, off)
	count := 0
	for _, item := range got {
		if item.Label == "$x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("$x should appear exactly once, got %d", count)
	}
}

func TestBlockScopedVarItems_ScopedToBlockNotDocument(t *testing.T) {
	// Two separate `object-templates-raw:` blocks in one document. A
	// variable declared in the first should NOT appear in completions
	// for a cursor inside the second.
	text := `---
spec:
  object-templates-raw: |
    {{- $first := "a" -}}
---
spec:
  object-templates-raw: |
    {{- $second := "b" -}}
    key: '{{ $`
	off := len(text)
	got := blockScopedVarItems(text, off)
	for _, item := range got {
		if item.Label == "$first" {
			t.Errorf("variable $first from a different block scalar should NOT appear in this block's completions")
		}
	}
	found := false
	for _, item := range got {
		if item.Label == "$second" {
			found = true
		}
	}
	if !found {
		t.Errorf("$second should appear in completions for the second block; got: %+v", got)
	}
}

func TestProvide_IncludesBlockVarsAlongsideCatalog(t *testing.T) {
	text := `spec:
  object-templates-raw: |
    {{- $obsConfig := lookup "v1" "ConfigMap" "ns" "n" -}}
    key: '{{`
	in := CompletionInput{
		Text:     text,
		Position: positionFromOffset(text, len(text)),
		Catalog: catalog.Resolved{
			HelmFunctions: []catalog.TemplateFunction{{Name: "include"}},
			GoBuiltins:    []catalog.TemplateFunction{{Name: "printf"}},
		},
	}
	items := Provide(in)
	hasInclude := false
	hasObsConfig := false
	for _, item := range items {
		switch item.Label {
		case "include":
			hasInclude = true
		case "$obsConfig":
			hasObsConfig = true
		}
	}
	if !hasInclude {
		t.Errorf("expected catalog function `include` in completions; got: %+v", items)
	}
	if !hasObsConfig {
		t.Errorf("expected block-scoped variable `$obsConfig` in completions; got: %+v", items)
	}
}

var _ = findOffset // referenced by potential future tests
