package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"gopkg.in/yaml.v3"
)

// flattener resolves aliases and merge keys (<<) into a plain tree, keeping
// each node's position, so every later step sees what YAML means. It records
// problems as it goes; flatten returns nil when the tree can't be read.
type flattener struct {
	nodes int
	ps    *problems
}

const (
	maxDepth = 100
	maxNodes = 1_000_000
)

func (f *flattener) flatten(n *yaml.Node, depth int) *yaml.Node {
	f.nodes++
	if depth > maxDepth || f.nodes > maxNodes {
		f.ps.put(Problem{Line: n.Line, Column: n.Column, Message: "the file nests or repeats aliases too deeply to read"})
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Line: n.Line, Column: n.Column}
		}
		return f.flatten(n.Content[0], depth+1)
	case yaml.AliasNode:
		return f.flatten(n.Alias, depth+1)
	case yaml.SequenceNode:
		out := *n
		out.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			fc := f.flatten(c, depth+1)
			if fc == nil {
				return nil
			}
			out.Content[i] = fc
		}
		return &out
	case yaml.MappingNode:
		return f.mapping(n, depth)
	case yaml.ScalarNode:
		out := *n
		return &out
	}
	out := *n
	return &out
}

// mapping flattens a map. A duplicate key is recorded and the last value
// kept, as the schema will see it; merge keys that aren't maps are fatal.
func (f *flattener) mapping(n *yaml.Node, depth int) *yaml.Node {
	explicit := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if isMerge(k) {
			continue
		}
		if explicit[k.Value] {
			f.ps.put(Problem{Line: k.Line, Column: k.Column, Message: fmt.Sprintf("key %q appears twice in the same map", k.Value)})
		}
		explicit[k.Value] = true
	}
	out := *n
	out.Content = nil
	added := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		fv := f.flatten(v, depth+1)
		if fv == nil {
			return nil
		}
		if !isMerge(k) {
			kc := *k
			out.Content = append(out.Content, &kc, fv)
			continue
		}
		var sources []*yaml.Node
		switch fv.Kind {
		case yaml.MappingNode:
			sources = []*yaml.Node{fv}
		case yaml.SequenceNode:
			sources = fv.Content
		default:
			return f.notAMap(fv)
		}
		for _, src := range sources {
			if src.Kind != yaml.MappingNode {
				return f.notAMap(src)
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
	return &out
}

func (f *flattener) notAMap(n *yaml.Node) *yaml.Node {
	f.ps.put(Problem{Line: n.Line, Column: n.Column, Message: "a merge key (<<) takes a map or a list of maps"})
	return nil
}

func isMerge(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Value == "<<" && (k.Tag == "!!merge" || k.Tag == "")
}

// toAny turns a flattened tree into the JSON-like values the schema
// validator reads. Map keys are taken as written. An integer past int64
// stays an exact number, so the schema judges its range; infinity and NaN
// have no JSON form, so they are problems here.
func (f *flattener) toAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = f.toAny(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		s := make([]any, len(n.Content))
		for i, c := range n.Content {
			s[i] = f.toAny(c)
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
			var u uint64
			if n.Decode(&u) == nil {
				return json.Number(strconv.FormatUint(u, 10))
			}
		case "!!float":
			var x float64
			if n.Decode(&x) == nil {
				if math.IsInf(x, 0) || math.IsNaN(x) {
					f.ps.put(Problem{Line: n.Line, Column: n.Column, Message: n.Value + " is not a finite number"})
				}
				return x
			}
		}
		return n.Value
	default:
	}
	return nil
}
