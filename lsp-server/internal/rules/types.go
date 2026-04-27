package rules

import (
	"github.com/acm-ls/lsp-server/internal/parsedoc"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Severity mirrors VSCode's, mapped to the LSP enum at emit time.
type Severity string

const (
	SeverityError       Severity = "error"
	SeverityWarning     Severity = "warning"
	SeverityInformation Severity = "information"
	SeverityHint        Severity = "hint"
)

func (s Severity) ToLSP() protocol.DiagnosticSeverity {
	switch s {
	case SeverityError:
		return protocol.DiagnosticSeverityError
	case SeverityWarning:
		return protocol.DiagnosticSeverityWarning
	case SeverityInformation:
		return protocol.DiagnosticSeverityInformation
	case SeverityHint:
		return protocol.DiagnosticSeverityHint
	}
	return protocol.DiagnosticSeverityWarning
}

// Settings is a generic bag the LSP client (Neovim) passes via initializationOptions
// or workspace/configuration. Keys mirror the VSCode extension's acm.* schema.
type Settings map[string]any

// Get walks a dotted path through the settings tree.
func Get[T any](s Settings, path string, fallback T) T {
	cur := any(map[string]any(s))
	for _, part := range splitDots(path) {
		m, ok := cur.(map[string]any)
		if !ok {
			return fallback
		}
		v, ok := m[part]
		if !ok {
			return fallback
		}
		cur = v
	}
	if cur == nil {
		return fallback
	}
	if v, ok := cur.(T); ok {
		return v
	}
	return fallback
}

// GetStringSlice extracts a []string from a generic settings tree (which arrives
// as []any from JSON unmarshal).
func GetStringSlice(s Settings, path string, fallback []string) []string {
	raw := Get[any](s, path, nil)
	if raw == nil {
		return fallback
	}
	arr, ok := raw.([]any)
	if !ok {
		return fallback
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// GetInt extracts an int from settings. JSON numbers come in as float64.
func GetInt(s Settings, path string, fallback int) int {
	raw := Get[any](s, path, nil)
	if raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return fallback
}

// Context is what a rule is given to evaluate.
type Context struct {
	URI      string
	FilePath string // filesystem path derived from URI when scheme is file://; empty otherwise.
	Text     string
	Docs     []parsedoc.ParsedDoc
	Settings Settings
}

// Rule is the interface every check implements.
type Rule interface {
	ID() string
	Run(ctx Context) []protocol.Diagnostic
}

func splitDots(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '.' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
