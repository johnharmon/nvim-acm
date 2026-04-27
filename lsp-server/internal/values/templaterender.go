package values

import (
	"regexp"
	"strings"
)

// Resolver fetches a Node for a dotted .Values path's segments.
type Resolver func(segments []string) *Node

var exprRE = regexp.MustCompile(`\{\{-?\s*([^}]*?)\s*-?\}\}`)

// RenderSimpleTemplate evaluates `.Values.x | default "y"`-style expressions
// in a template string. Returns the rendered string if every expression
// resolves; nil if any can't be rendered (Helm variables, function calls, etc.).
func RenderSimpleTemplate(tmpl string, resolve Resolver) (string, bool) {
	var result strings.Builder
	last := 0
	matches := exprRE.FindAllStringSubmatchIndex(tmpl, -1)
	for _, m := range matches {
		start, end := m[0], m[1]
		exprStart, exprEnd := m[2], m[3]
		result.WriteString(tmpl[last:start])
		rendered, ok := renderExpr(tmpl[exprStart:exprEnd], resolve)
		if !ok {
			return "", false
		}
		result.WriteString(rendered)
		last = end
	}
	result.WriteString(tmpl[last:])
	return result.String(), true
}

func renderExpr(expr string, resolve Resolver) (string, bool) {
	stages := splitPipeline(expr)
	if len(stages) == 0 {
		return "", false
	}

	first := stages[0]
	segments, ok := parseValuesPath(first)
	if !ok {
		return "", false
	}
	node := resolve(segments)
	var value string
	hasValue := false
	if node != nil && node.Type != TypeMap && node.Type != TypeList && node.Example != "" {
		value = node.Example
		hasValue = true
	}

	for i := 1; i < len(stages); i++ {
		stage := strings.TrimSpace(stages[i])
		if def, ok := parseDefault(stage); ok {
			if !hasValue || value == "" {
				value = def
				hasValue = true
			}
			continue
		}
		switch stage {
		case "quote":
			if !hasValue {
				return "", false
			}
			value = `"` + value + `"`
		case "squote":
			if !hasValue {
				return "", false
			}
			value = "'" + value + "'"
		default:
			return "", false
		}
	}

	if !hasValue {
		return "", false
	}
	return value, true
}

func parseValuesPath(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	prefix := ""
	switch {
	case strings.HasPrefix(s, ".Values."):
		prefix = ".Values."
	case strings.HasPrefix(s, "$.Values."):
		prefix = "$.Values."
	default:
		return nil, false
	}
	rest := s[len(prefix):]
	if rest == "" {
		return nil, false
	}
	for _, r := range rest {
		if !isPathChar(r) {
			return nil, false
		}
	}
	return strings.Split(rest, "."), true
}

var defaultRE = regexp.MustCompile(`^default\s+(".*?"|'.*?'|\S+)$`)

func parseDefault(stage string) (string, bool) {
	m := defaultRE.FindStringSubmatch(stage)
	if m == nil {
		return "", false
	}
	return stripQuotes(m[1]), true
}

func splitPipeline(expr string) []string {
	stages := []string{}
	depth := 0
	inString := false
	quote := byte(0)
	start := 0
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if inString {
			if c == '\\' && quote == '"' && i+1 < len(expr) {
				i++
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inString = true
			quote = c
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
		} else if c == '|' && depth == 0 {
			stages = append(stages, strings.TrimSpace(expr[start:i]))
			start = i + 1
		}
	}
	stages = append(stages, strings.TrimSpace(expr[start:]))
	out := stages[:0]
	for _, s := range stages {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isPathChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
}
