package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Load reads and validates the config file at path. Errors from the file
// itself are an *Error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse validates a config held in memory. file names it in errors.
func Parse(file string, data []byte) (*Config, error) {
	cfg, ps := parse(data)
	if err := ps.err(file); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parse reports every problem it can see at once. Only invalid YAML and a
// tree too deep to read stop it early, because nothing after them can be
// trusted.
func parse(data []byte) (*Config, *problems) {
	ps := &problems{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		ps.put(yamlProblem(err))
		return nil, ps
	}
	if len(doc.Content) == 0 {
		ps.put(Problem{Message: "the file is empty; it needs at least system and traffic"})
		return nil, ps
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		ps.put(Problem{Message: "the file has more than one YAML document; kazu.yaml is one"})
		return nil, ps
	}
	f := flattener{ps: ps}
	root := f.flatten(doc.Content[0], 0)
	if root == nil {
		return nil, ps
	}
	ps.root = root
	if root.Kind != yaml.MappingNode {
		ps.add(nil, "the file must be a map of keys such as system, traffic and edges")
		return nil, ps
	}
	before := len(ps.list)
	tree := f.toAny(root)
	if len(ps.list) > before {
		return nil, ps
	}
	if err := compiledSchema().Validate(tree); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			ps.add(nil, err.Error())
			return nil, ps
		}
		schemaProblems(ps, ve)
	}
	if len(ps.list) > 0 {
		return nil, ps
	}
	d := decoder{ps: ps}
	cfg := d.config(root)
	if len(ps.list) > 0 {
		return nil, ps
	}
	return cfg, ps
}

var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// yamlProblem reads the position out of yaml.v3's error text, the only
// place the library puts it.
func yamlProblem(err error) Problem {
	msg := err.Error()
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Line: line, Message: "invalid YAML: " + m[2]}
	}
	return Problem{Message: "invalid YAML: " + strings.TrimPrefix(msg, "yaml: ")}
}
