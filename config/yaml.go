package config

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// flattener resolves aliases and merge keys (<<) into a plain tree, keeping
// each node's position, so every later step sees what YAML means.
type flattener struct{ nodes int }

const (
	maxDepth = 100
	maxNodes = 1_000_000
)

func (f *flattener) flatten(n *yaml.Node, depth int) (*yaml.Node, error) {
	f.nodes++
	if depth > maxDepth || f.nodes > maxNodes {
		return nil, errors.New("the file nests or repeats aliases too deeply to read")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Line: n.Line, Column: n.Column}, nil
		}
		return f.flatten(n.Content[0], depth+1)
	case yaml.AliasNode:
		return f.flatten(n.Alias, depth+1)
	case yaml.SequenceNode:
		out := *n
		out.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			fc, err := f.flatten(c, depth+1)
			if err != nil {
				return nil, err
			}
			out.Content[i] = fc
		}
		return &out, nil
	case yaml.MappingNode:
		return f.mapping(n, depth)
	case yaml.ScalarNode:
		out := *n
		return &out, nil
	}
	out := *n
	return &out, nil
}

func (f *flattener) mapping(n *yaml.Node, depth int) (*yaml.Node, error) {
	explicit := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; !isMerge(k) {
			explicit[k.Value] = true
		}
	}
	out := *n
	out.Content = nil
	added := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !isMerge(k) {
			fv, err := f.flatten(v, depth+1)
			if err != nil {
				return nil, err
			}
			kc := *k
			out.Content = append(out.Content, &kc, fv)
			continue
		}
		fv, err := f.flatten(v, depth+1)
		if err != nil {
			return nil, err
		}
		var sources []*yaml.Node
		switch fv.Kind {
		case yaml.MappingNode:
			sources = []*yaml.Node{fv}
		case yaml.SequenceNode:
			sources = fv.Content
		default:
			return nil, fmt.Errorf("line %d: a merge key (<<) takes a map or a list of maps", fv.Line)
		}
		for _, src := range sources {
			if src.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("line %d: a merge key (<<) takes a map or a list of maps", src.Line)
			}
			for j := 0; j+1 < len(src.Content); j += 2 {
				key := src.Content[j].Value
				if explicit[key] || added[key] {
					continue
				}
				added[key] = true
				out.Content = append(out.Content, src.Content[j], src.Content[j+1])
			}
		}
	}
	return &out, nil
}

func isMerge(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Value == "<<" && (k.Tag == "!!merge" || k.Tag == "")
}

// toAny turns a flattened tree into the JSON-like values the schema
// validator reads. Map keys are taken as written.
func toAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = toAny(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		s := make([]any, len(n.Content))
		for i, c := range n.Content {
			s[i] = toAny(c)
		}
		return s
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil
		case "!!bool":
			var b bool
			if n.Decode(&b) == nil {
				return b
			}
		case "!!int":
			var i int64
			if n.Decode(&i) == nil {
				return i
			}
		case "!!float":
			var f float64
			if n.Decode(&f) == nil {
				return f
			}
		}
		return n.Value
	default:
	}
	return nil
}
