package values

import "strings"

// ValuesPath is the parsed `.Values.foo.bar|cursor` chain — segments
// already-typed plus the partial prefix at the cursor.
type ValuesPath struct {
	Segments []string
	Prefix   string
}

// ParseValuesPathBeforeCursor extracts the path expression to the left of
// offset. Returns (path, true) only if the cursor sits on a `.Values.<...>` chain.
func ParseValuesPathBeforeCursor(text string, offset int) (ValuesPath, bool) {
	i := offset
	for i > 0 {
		c := text[i-1]
		if isPathContinuation(c) {
			i--
			continue
		}
		break
	}
	rawPath := text[i:offset]
	if rawPath == "" {
		return ValuesPath{}, false
	}

	var inner string
	switch {
	case strings.HasPrefix(rawPath, ".Values."):
		inner = rawPath[len(".Values."):]
	case strings.HasPrefix(rawPath, "$.Values."):
		inner = rawPath[len("$.Values."):]
	default:
		return ValuesPath{}, false
	}

	endsWithDot := strings.HasSuffix(inner, ".")
	cleaned := inner
	if endsWithDot {
		cleaned = inner[:len(inner)-1]
	}
	var parts []string
	if cleaned != "" {
		parts = strings.Split(cleaned, ".")
	}
	if endsWithDot || inner == "" {
		return ValuesPath{Segments: parts, Prefix: ""}, true
	}
	if len(parts) == 0 {
		return ValuesPath{}, false
	}
	return ValuesPath{Segments: parts[:len(parts)-1], Prefix: parts[len(parts)-1]}, true
}

func isPathContinuation(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '.' || c == '$'
}
