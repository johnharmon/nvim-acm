package values

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ValueType is the inferred kind of a values.yaml node.
type ValueType string

const (
	TypeString  ValueType = "string"
	TypeNumber  ValueType = "number"
	TypeBoolean ValueType = "boolean"
	TypeNull    ValueType = "null"
	TypeMap     ValueType = "map"
	TypeList    ValueType = "list"
	TypeUnknown ValueType = "unknown"
)

// Node is the parsed values.yaml tree the rules walk.
type Node struct {
	Type        ValueType
	Description string
	Example     string
	Children    map[string]*Node
}

// FindChartRoot walks up from filePath looking for Chart.yaml. Returns "" if not found.
func FindChartRoot(filePath string) string {
	dir := filepath.Dir(filePath)
	for {
		if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadChartValues parses chartRoot/values.yaml.
func LoadChartValues(chartRoot string) *Node {
	return LoadValuesFile(filepath.Join(chartRoot, "values.yaml"))
}

// LoadValuesFile parses any values.yaml-shaped file.
func LoadValuesFile(path string) *Node {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	root := unwrapDocument(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	return toNode(root)
}

func toNode(n *yaml.Node) *Node {
	if n == nil {
		return &Node{Type: TypeNull}
	}
	switch n.Kind {
	case yaml.MappingNode:
		children := map[string]*Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			v := n.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			child := toNode(v)
			if k.HeadComment != "" {
				child.Description = trimCommentLines(k.HeadComment)
			}
			children[k.Value] = child
		}
		return &Node{Type: TypeMap, Children: children}
	case yaml.SequenceNode:
		return &Node{Type: TypeList, Example: previewList(n)}
	case yaml.ScalarNode:
		return scalarNode(n)
	}
	return &Node{Type: TypeUnknown}
}

func scalarNode(n *yaml.Node) *Node {
	switch n.Tag {
	case "!!str":
		return &Node{Type: TypeString, Example: n.Value}
	case "!!int", "!!float":
		return &Node{Type: TypeNumber, Example: n.Value}
	case "!!bool":
		return &Node{Type: TypeBoolean, Example: n.Value}
	case "!!null", "":
		if n.Value == "" || n.Value == "null" || n.Value == "~" {
			return &Node{Type: TypeNull}
		}
		return &Node{Type: TypeString, Example: n.Value}
	}
	return &Node{Type: TypeUnknown, Example: n.Value}
}

func previewList(n *yaml.Node) string {
	max := 3
	if len(n.Content) < max {
		max = len(n.Content)
	}
	parts := make([]string, 0, max)
	for i := 0; i < max; i++ {
		c := n.Content[i]
		switch c.Kind {
		case yaml.ScalarNode:
			parts = append(parts, c.Value)
		case yaml.MappingNode:
			parts = append(parts, "{...}")
		case yaml.SequenceNode:
			parts = append(parts, "[...]")
		default:
			parts = append(parts, "?")
		}
	}
	suffix := ""
	if len(n.Content) > max {
		suffix = "..."
	}
	return "[" + strings.Join(parts, ", ") + suffix + "]"
}

func trimCommentLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(l), "#"), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func unwrapDocument(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// Navigate drills through a node tree by segments and returns the deepest node.
// Returns nil if any segment misses or goes through a non-map.
func Navigate(root *Node, segments []string) *Node {
	cur := root
	for _, seg := range segments {
		if cur == nil || cur.Type != TypeMap || cur.Children == nil {
			return nil
		}
		cur = cur.Children[seg]
	}
	return cur
}

// Cache layers chart values + overlay files, invalidating on mtime changes.
type Cache struct {
	mu           sync.Mutex
	overlayPaths []string
	entries      map[string]cacheEntry
}

type cacheEntry struct {
	signature string
	root      *Node
}

func NewCache() *Cache {
	return &Cache{entries: map[string]cacheEntry{}}
}

func (c *Cache) SetOverlayPaths(paths []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overlayPaths = paths
	c.entries = map[string]cacheEntry{}
}

// Get returns the merged tree for chartRoot, layering overlays in order.
func (c *Cache) Get(chartRoot string) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	paths := append([]string{filepath.Join(chartRoot, "values.yaml")}, c.overlayPaths...)
	sig := signature(paths)
	if entry, ok := c.entries[chartRoot]; ok && entry.signature == sig {
		return entry.root
	}
	nodes := []*Node{}
	for _, p := range paths {
		if n := LoadValuesFile(p); n != nil {
			nodes = append(nodes, n)
		}
	}
	merged := MergeAll(nodes)
	c.entries[chartRoot] = cacheEntry{signature: sig, root: merged}
	return merged
}

// Clear drops every cached entry.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}

func signature(paths []string) string {
	parts := make([]string, len(paths))
	for i, p := range paths {
		stat, err := os.Stat(p)
		if err != nil {
			parts[i] = p + ":missing"
			continue
		}
		parts[i] = p + ":" + stat.ModTime().UTC().String()
	}
	return strings.Join(parts, "|")
}

// URIToPath converts a `file://...` LSP URI to a filesystem path. Returns empty
// string if the URI isn't a file:// scheme.
func URIToPath(rawURI string) string {
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}
