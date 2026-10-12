package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

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
	d := decoder{}
	cfg := d.config(root)
	validate(ps, root, cfg)
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
