package parsedoc

import (
	"regexp"
)

// BlockScalarSpan is the byte range of a YAML literal block-scalar's
// content (the body after the `|` indicator), recovered from the raw
// document text with original indentation preserved so error/parse
// positions reported within the body map directly back to document
// lines.
type BlockScalarSpan struct {
	ContentStart int // absolute byte offset of first content line
	ContentEnd   int // absolute byte offset just past the last content byte
	ContentLine  int // 0-indexed document line of the first content line
	KeyIndent    int // number of leading spaces on the `<key>: |` line
}

// blockKeyRE matches an `object-templates-raw:` line whose value is a
// literal block-scalar (`|`, `|+`, `|-`, `|<digit>`, etc.). Folded
// (`>`) variants don't appear in real ACM policies and would produce
// different effective content (paragraph-folded), so we skip them.
var blockKeyRE = regexp.MustCompile(`(?m)^(\s*)object-templates-raw:\s*\|[+-]?\d*\s*$`)

// FindObjectTemplatesRawBlocks scans `text` and returns every
// `object-templates-raw:` literal block scalar with its content range.
// Walks line-by-line: locate the `<key>: |` line, then collect lines
// whose indent is strictly greater than the key's indent (blank lines
// included). The first line whose indent is `<= keyIndent` ends the
// block.
func FindObjectTemplatesRawBlocks(text string) []BlockScalarSpan {
	lines := SplitDocLines(text)
	out := []BlockScalarSpan{}
	for i, ln := range lines {
		m := blockKeyRE.FindStringSubmatch(ln.Text)
		if m == nil {
			continue
		}
		keyIndent := len(m[1])
		if i+1 >= len(lines) {
			continue
		}
		contentLine := i + 1
		contentStart := lines[contentLine].Offset
		contentEnd := contentStart
		j := contentLine
		for j < len(lines) {
			lt := lines[j]
			if isBlankLine(lt.Text) {
				contentEnd = lt.Offset + lt.TotalLen
				j++
				continue
			}
			if leadingSpaces(lt.Text) <= keyIndent {
				break
			}
			contentEnd = lt.Offset + lt.TotalLen
			j++
		}
		if contentEnd > contentStart {
			out = append(out, BlockScalarSpan{
				ContentStart: contentStart,
				ContentEnd:   contentEnd,
				ContentLine:  contentLine,
				KeyIndent:    keyIndent,
			})
		}
	}
	return out
}

// ContainingObjectTemplatesRawBlock returns the block-scalar span whose
// content range contains `offset`, or `false` if `offset` isn't inside
// any object-templates-raw block.
func ContainingObjectTemplatesRawBlock(text string, offset int) (BlockScalarSpan, bool) {
	for _, b := range FindObjectTemplatesRawBlocks(text) {
		if offset >= b.ContentStart && offset <= b.ContentEnd {
			return b, true
		}
	}
	return BlockScalarSpan{}, false
}

// DocLine is a single line plus its byte offset and full length
// (including any trailing line terminator).
type DocLine struct {
	Offset   int    // byte offset of the line's first character
	Text     string // line content excluding any trailing line terminator
	TotalLen int    // bytes including the trailing `\n` (or `\r\n`) if present
}

// SplitDocLines splits `text` into lines preserving CRLF and offsets
// so byte positions in the original text can be recovered from
// line-relative positions.
func SplitDocLines(text string) []DocLine {
	out := []DocLine{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			content := text[start:i]
			if len(content) > 0 && content[len(content)-1] == '\r' {
				content = content[:len(content)-1]
			}
			out = append(out, DocLine{Offset: start, Text: content, TotalLen: i - start + 1})
			start = i + 1
		}
	}
	if start < len(text) {
		content := text[start:]
		if len(content) > 0 && content[len(content)-1] == '\r' {
			content = content[:len(content)-1]
		}
		out = append(out, DocLine{Offset: start, Text: content, TotalLen: len(text) - start})
	}
	return out
}

func leadingSpaces(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return len(s)
}

func isBlankLine(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
}
