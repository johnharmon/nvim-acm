package values

import "testing"

func mkMap(entries map[string]*Node) *Node {
	return &Node{Type: TypeMap, Children: entries}
}
func mkStr(v string) *Node { return &Node{Type: TypeString, Example: v} }

func equalNodes(a, b *Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type != b.Type {
		return false
	}
	if a.Type != TypeMap {
		return a.Example == b.Example
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for k, v := range a.Children {
		if !equalNodes(v, b.Children[k]) {
			return false
		}
	}
	return true
}

func TestMergeValues(t *testing.T) {
	cases := []struct {
		name           string
		base, overlay  *Node
		expected       *Node
	}{
		{
			"overlay replaces leaf",
			mkMap(map[string]*Node{"ns": mkStr("a")}),
			mkMap(map[string]*Node{"ns": mkStr("b")}),
			mkMap(map[string]*Node{"ns": mkStr("b")}),
		},
		{
			"overlay adds new key",
			mkMap(map[string]*Node{"a": mkStr("1")}),
			mkMap(map[string]*Node{"b": mkStr("2")}),
			mkMap(map[string]*Node{"a": mkStr("1"), "b": mkStr("2")}),
		},
		{
			"deep merge",
			mkMap(map[string]*Node{"acm": mkMap(map[string]*Node{
				"version":   mkStr("2.14"),
				"namespace": mkStr("n"),
			})}),
			mkMap(map[string]*Node{"acm": mkMap(map[string]*Node{"version": mkStr("2.15")})}),
			mkMap(map[string]*Node{"acm": mkMap(map[string]*Node{
				"version":   mkStr("2.15"),
				"namespace": mkStr("n"),
			})}),
		},
		{
			"scalar overlay replaces map",
			mkMap(map[string]*Node{"x": mkMap(map[string]*Node{"y": mkStr("1")})}),
			mkMap(map[string]*Node{"x": mkStr("plain")}),
			mkMap(map[string]*Node{"x": mkStr("plain")}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeValues(c.base, c.overlay)
			if !equalNodes(got, c.expected) {
				t.Fatalf("merge mismatch")
			}
		})
	}
}

func TestMergeAllOrdering(t *testing.T) {
	got := MergeAll([]*Node{
		mkMap(map[string]*Node{"ns": mkStr("a")}),
		mkMap(map[string]*Node{"ns": mkStr("b")}),
		mkMap(map[string]*Node{"ns": mkStr("c")}),
	})
	expected := mkMap(map[string]*Node{"ns": mkStr("c")})
	if !equalNodes(got, expected) {
		t.Fatalf("last should win")
	}
}

func TestMergeAllEmpty(t *testing.T) {
	if MergeAll(nil) != nil {
		t.Fatalf("empty merge should be nil")
	}
}
