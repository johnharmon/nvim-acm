package providers

import (
	"strings"
	"testing"

	"github.com/autoshift/lsp-server/internal/catalog"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// catalogWithHubValues returns a catalog that includes the canonical
// hub-side exported values so completion tests can assert against them.
func catalogWithHubValues() catalog.Resolved {
	return catalog.Resolved{
		HubFunctions: []catalog.TemplateFunction{
			{Name: "fromSecret", Signature: "fromSecret(ns, name, key)"},
		},
		HubExportedValues: []catalog.ExportedValue{
			{Name: ".ManagedClusterName", Type: "string", Description: "name of the cluster"},
			{Name: ".ManagedClusterLabels", Type: "map[string]string", Description: "labels"},
			{Name: ".PolicyMetadata", Type: "map[string]any", Description: "root policy metadata"},
		},
	}
}

// findItem returns the first completion item with the given label.
func findItem(items []protocol.CompletionItem, label string) *protocol.CompletionItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

func TestCompletion_HubExportedValues_AppearAndFilterCorrectly(t *testing.T) {
	// Cursor inside a {{hub ... hub}} block at offset that matches `.M` typed.
	// `apiVersion: ...{{hub .M hub}}` — we put cursor at character 8 on the
	// line below, which is the 'M' position so the layer detector reports hub.
	text := "spec:\n  body: '{{hub .M hub}}'\n"
	// Find cursor offset: after the M.
	cursorIdx := strings.Index(text, ".M") + 2 // after "M"
	// Convert to line/char.
	prefix := text[:cursorIdx]
	line := uint32(strings.Count(prefix, "\n"))
	lastNL := strings.LastIndex(prefix, "\n")
	col := uint32(len(prefix) - lastNL - 1)

	in := CompletionInput{
		URI:      "file:///tmp/t.yaml",
		FilePath: "",
		Text:     text,
		Position: protocol.Position{Line: line, Character: col},
		Catalog:  catalogWithHubValues(),
	}
	items := Provide(in)

	managedLabels := findItem(items, ".ManagedClusterLabels")
	if managedLabels == nil {
		var labels []string
		for _, it := range items {
			labels = append(labels, it.Label)
		}
		t.Fatalf(".ManagedClusterLabels not in completion items. got %d items: %v",
			len(items), labels)
	}

	// The fix: FilterText and InsertText should be set to the bare name
	// (without leading dot) so LSP clients filter against `M`-prefix correctly.
	if managedLabels.FilterText == nil || *managedLabels.FilterText != "ManagedClusterLabels" {
		t.Errorf("FilterText: got %v, want %q", managedLabels.FilterText, "ManagedClusterLabels")
	}
	if managedLabels.InsertText == nil || *managedLabels.InsertText != "ManagedClusterLabels" {
		t.Errorf("InsertText: got %v, want %q", managedLabels.InsertText, "ManagedClusterLabels")
	}
}

func TestCompletion_HubFunctions_AppearInHubContext(t *testing.T) {
	text := "spec:\n  body: '{{hub from hub}}'\n"
	cursorIdx := strings.Index(text, "from") + 4
	prefix := text[:cursorIdx]
	line := uint32(strings.Count(prefix, "\n"))
	lastNL := strings.LastIndex(prefix, "\n")
	col := uint32(len(prefix) - lastNL - 1)

	items := Provide(CompletionInput{
		URI:      "file:///tmp/t.yaml",
		Text:     text,
		Position: protocol.Position{Line: line, Character: col},
		Catalog:  catalogWithHubValues(),
	})

	if findItem(items, "fromSecret") == nil {
		t.Errorf("fromSecret not in items at hub-context cursor (got %d items)", len(items))
	}
}
