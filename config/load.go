package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Schema is the published JSON schema for kazu.yaml, for editors and for
// anyone generating configs.
//
//go:embed kazu.schema.json
var Schema []byte

const schemaURL = "https://zugzwang-io.github.io/kazu/kazu.schema.json"

// Load reads and validates the config file at path. Relative paths in it
// (the system file) are resolved against the file's directory. Errors from
// the file itself are an *Error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, ps := parse(data)
	if cfg != nil {
		checkSystemExists(ps, cfg, filepath.Dir(path))
	}
	if err := ps.err(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Parse validates a config held in memory. file names it in errors. Paths
// in the config are not checked; Load does that.
func Parse(file string, data []byte) (*Config, error) {
	cfg, ps := parse(data)
	if err := ps.err(file); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parse(data []byte) (*Config, *problems) {
	ps := &problems{}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		ps.list = append(ps.list, yamlProblem(err))
		return nil, ps
	}
	if len(doc.Content) == 0 {
		ps.list = append(ps.list, Problem{Message: "the file is empty; it needs at least system and traffic"})
		return nil, ps
	}
	f := flattener{}
	root, err := f.flatten(doc.Content[0], 0)
	if err != nil {
		ps.list = append(ps.list, Problem{Line: doc.Content[0].Line, Column: doc.Content[0].Column, Message: err.Error()})
		return nil, ps
	}
	ps.root = root
	if len(f.dups) > 0 {
		ps.list = append(ps.list, f.dups...)
		return nil, ps
	}
	if root.Kind != yaml.MappingNode {
		ps.add(nil, "the file must be a map of keys such as system, traffic and edges")
		return nil, ps
	}
	if err := compiledSchema().Validate(toAny(root)); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			ps.add(nil, err.Error())
			return nil, ps
		}
		schemaProblems(ps, ve)
		return nil, ps
	}
	d := decoder{ps: ps}
	cfg := d.config(root)
	return cfg, ps
}

var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

func yamlProblem(err error) Problem {
	msg := err.Error()
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		msg = te.Errors[0]
	}
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Line: line, Column: 1, Message: "invalid YAML: " + m[2]}
	}
	return Problem{Message: "invalid YAML: " + strings.TrimPrefix(msg, "yaml: ")}
}

func checkSystemExists(ps *problems, cfg *Config, dir string) {
	rel, path := cfg.System.Compose, []string{"system"}
	if rel == "" {
		rel, path = cfg.System.Manifests, []string{"system", "manifests"}
	}
	full := rel
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, rel)
	}
	st, err := os.Stat(full)
	switch {
	case err != nil:
		ps.add(path, fmt.Sprintf("%s not found (paths are relative to the config file)", rel))
	case cfg.System.Manifests != "" && !st.IsDir():
		ps.add(path, fmt.Sprintf("%s is not a directory; manifests names a directory of rendered manifests", rel))
	case cfg.System.Compose != "" && st.IsDir():
		ps.add(path, fmt.Sprintf("%s is a directory; for rendered manifests write system: { manifests: %s }", rel, rel))
	}
}

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaDoc  any
)

func compiledSchema() *jsonschema.Schema {
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema))
		if err != nil {
			panic("config: kazu.schema.json: " + err.Error())
		}
		schemaDoc = doc
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schemaURL, doc); err != nil {
			panic("config: kazu.schema.json: " + err.Error())
		}
		schema = c.MustCompile(schemaURL)
	})
	return schema
}

// flattener resolves aliases and merge keys (<<) into a plain tree, keeping
// each node's position, so every later step sees what YAML means.
type flattener struct {
	nodes int
	dups  []Problem
}

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
		k := n.Content[i]
		if isMerge(k) {
			continue
		}
		if explicit[k.Value] {
			f.dups = append(f.dups, Problem{Line: k.Line, Column: k.Column, Message: fmt.Sprintf("key %q appears twice in the same map", k.Value)})
		}
		explicit[k.Value] = true
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
