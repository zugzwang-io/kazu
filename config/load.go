package config

import (
	"errors"
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
	d := decoder{}
	return d.config(root), ps
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
