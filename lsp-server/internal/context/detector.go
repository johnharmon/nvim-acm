package context

import (
	"regexp"
	"strings"
)

// Layer is the current Go-template processing layer at a cursor position.
type Layer string

const (
	LayerNone    Layer = "none"
	LayerHelm    Layer = "helm"
	LayerHub     Layer = "hub"
	LayerManaged Layer = "managed"
)

// LayerContext carries everything the completion/hover/signature-help paths
// need to decide what's available at the cursor.
type LayerContext struct {
	Layer                    Layer
	MustacheStart            int
	MustacheEnd              int // -1 if unclosed
	InStringLiteral          bool
	InsideObjectTemplatesRaw bool
}

// DetectLayerAt determines the layer at a byte offset.
func DetectLayerAt(text string, offset int) LayerContext {
	scan := scanToOffset(text, offset)
	insideRaw := IsInsideObjectTemplatesRaw(text, offset)

	if scan.mustacheStart == -1 {
		// Plain text between Helm expressions. In the double-split hub escape
		// form `{{ "{{hub" }} CONTENT {{ "hub}}" }}`, that CONTENT is hub-side
		// template text — so check the hub-span finder before falling through
		// to managed.
		if IsInsideAnyHubSpan(FindHubSpans(text), offset) {
			return LayerContext{
				Layer:                    LayerHub,
				MustacheStart:            -1,
				MustacheEnd:              -1,
				InsideObjectTemplatesRaw: insideRaw,
			}
		}
		if insideRaw {
			return LayerContext{
				Layer:                    LayerManaged,
				MustacheStart:            -1,
				MustacheEnd:              -1,
				InsideObjectTemplatesRaw: true,
			}
		}
		return LayerContext{
			Layer:         LayerNone,
			MustacheStart: -1,
			MustacheEnd:   -1,
		}
	}

	endOpener := scan.mustacheStart + 8
	if endOpener > len(text) {
		endOpener = len(text)
	}
	opener := text[scan.mustacheStart:endOpener]
	isHubMustache := hubOpener.MatchString(opener)

	if scan.inString {
		if isHubMustache {
			return buildCtx(LayerHub, scan, text, insideRaw, true)
		}
		stringPrefix := text[scan.stringStart+1 : offset]
		if hubInString.MatchString(stringPrefix) {
			return buildCtx(LayerHub, scan, text, insideRaw, true)
		}
		if strings.Contains(stringPrefix, "{{") && insideRaw {
			return buildCtx(LayerManaged, scan, text, insideRaw, true)
		}
		return buildCtx(LayerHelm, scan, text, insideRaw, true)
	}

	if isHubMustache {
		return buildCtx(LayerHub, scan, text, insideRaw, false)
	}
	return buildCtx(LayerHelm, scan, text, insideRaw, false)
}

var (
	hubOpener   = regexp.MustCompile(`^\{\{-?\s*hub\b`)
	hubInString = regexp.MustCompile(`\{\{-?\s*hub\b`)
)

type scanState struct {
	mustacheStart int
	inString      bool
	stringStart   int
}

// scanToOffset replays the text up to the cursor, tracking whether we're
// inside a `{{ ... }}` and whether we're inside a string literal within it.
func scanToOffset(text string, offset int) scanState {
	mustacheStart := -1
	inString := false
	var stringChar byte
	stringStart := -1

	i := 0
	for i < offset && i < len(text) {
		c := text[i]
		if mustacheStart == -1 {
			if c == '{' && i+1 < len(text) && text[i+1] == '{' {
				mustacheStart = i
				i += 2
				continue
			}
			i++
			continue
		}
		if !inString {
			if c == '}' && i+1 < len(text) && text[i+1] == '}' {
				mustacheStart = -1
				i += 2
				continue
			}
			if c == '"' || c == '`' {
				inString = true
				stringChar = c
				stringStart = i
			}
			i++
			continue
		}
		if c == '\\' && stringChar == '"' && i+1 < offset {
			i += 2
			continue
		}
		if c == stringChar {
			inString = false
			stringStart = -1
		}
		i++
	}
	return scanState{mustacheStart: mustacheStart, inString: inString, stringStart: stringStart}
}

func buildCtx(layer Layer, scan scanState, text string, insideRaw, inStr bool) LayerContext {
	closeIdx := strings.Index(text[scan.mustacheStart+2:], "}}")
	end := -1
	if closeIdx != -1 {
		end = scan.mustacheStart + 2 + closeIdx
	}
	return LayerContext{
		Layer:                    layer,
		MustacheStart:            scan.mustacheStart,
		MustacheEnd:              end,
		InStringLiteral:          inStr,
		InsideObjectTemplatesRaw: insideRaw,
	}
}

// IsInsideObjectTemplatesRaw walks YAML indentation backwards looking for an
// `object-templates-raw:` ancestor with strictly less indent than the cursor's.
func IsInsideObjectTemplatesRaw(text string, offset int) bool {
	if offset > len(text) {
		offset = len(text)
	}
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	cursorLineText := text[lineStart:offset]
	cursorIndent := indentOf(cursorLineText)
	if cursorIndent == -1 {
		prevIndent := findPrevNonEmptyLineIndent(text, lineStart)
		if prevIndent == -1 {
			return false
		}
		return walkAncestors(text, lineStart, prevIndent)
	}
	if cursorIndent == 0 {
		return false
	}
	return walkAncestors(text, lineStart, cursorIndent)
}

func indentOf(line string) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return -1
}

func findPrevNonEmptyLineIndent(text string, lineStart int) int {
	pos := lineStart - 1
	for pos > 0 {
		prevStart := strings.LastIndexByte(text[:pos], '\n') + 1
		line := text[prevStart:pos]
		ind := indentOf(line)
		if ind != -1 {
			return ind
		}
		pos = prevStart - 1
	}
	return -1
}

var keyMatch = regexp.MustCompile(`^\s*([A-Za-z_][\w.\-]*)\s*:`)

func walkAncestors(text string, cursorLineStart, cursorIndent int) bool {
	currentIndent := cursorIndent
	pos := cursorLineStart - 1
	for pos > 0 {
		lineStart := strings.LastIndexByte(text[:pos], '\n') + 1
		line := text[lineStart:pos]
		ind := indentOf(line)
		if ind == -1 || ind >= currentIndent {
			pos = lineStart - 1
			continue
		}
		m := keyMatch.FindStringSubmatch(line)
		if m != nil && m[1] == "object-templates-raw" {
			return true
		}
		currentIndent = ind
		pos = lineStart - 1
	}
	return false
}
