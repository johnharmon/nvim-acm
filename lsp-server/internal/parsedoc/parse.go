package parsedoc

import (
	"io"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"gopkg.in/yaml.v3"
)

// ParsedDoc captures the bits of a Kubernetes-style YAML doc the rules need.
type ParsedDoc struct {
	Kind          string
	Name          string
	Namespace     string
	NameNode      *yaml.Node
	KindNode      *yaml.Node
	NamespaceNode *yaml.Node
}

// ParseAll splits text into individual YAML documents and extracts kind+name.
func ParseAll(text string) []ParsedDoc {
	out := []ParsedDoc{}
	dec := yaml.NewDecoder(strings.NewReader(text))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		root := unwrapDocument(&doc)
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		pd := ParsedDoc{}
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i]
			val := root.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				continue
			}
			switch key.Value {
			case "kind":
				if val.Kind == yaml.ScalarNode {
					pd.Kind = val.Value
					pd.KindNode = val
				}
			case "metadata":
				if val.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(val.Content); j += 2 {
						mk := val.Content[j]
						mv := val.Content[j+1]
						if mk.Kind != yaml.ScalarNode || mv.Kind != yaml.ScalarNode {
							continue
						}
						switch mk.Value {
						case "name":
							pd.Name = mv.Value
							pd.NameNode = mv
						case "namespace":
							pd.Namespace = mv.Value
							pd.NamespaceNode = mv
						}
					}
				}
			}
		}
		out = append(out, pd)
	}
	return out
}

// RangeFromNode converts a yaml.Node's 1-indexed line/column to a 0-indexed LSP range.
func RangeFromNode(node *yaml.Node) protocol.Range {
	if node == nil {
		return protocol.Range{}
	}
	startLine := uint32(node.Line - 1)
	startCol := uint32(node.Column - 1)
	endCol := startCol + uint32(len(node.Value))
	return protocol.Range{
		Start: protocol.Position{Line: startLine, Character: startCol},
		End:   protocol.Position{Line: startLine, Character: endCol},
	}
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
