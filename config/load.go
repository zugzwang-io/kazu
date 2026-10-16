package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// the file itself are an *Error, including anything the schema allows that
// this build doesn't support yet.
func Load(path string) (*Config, error) { return load(path, true) }

// load reads the file at path; built adds the not-supported-yet check.
func load(path string, built bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, ps := parse(data, built)
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
func Parse(file string, data []byte) (*Config, error) { return parseFile(file, data, true) }

func parseFile(file string, data []byte, built bool) (*Config, error) {
	cfg, ps := parse(data, built)
	if err := ps.err(file); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parse reports every problem it can see at once. Only invalid YAML and a
// tree too deep to read stop it early, because nothing after them can be
// trusted; the cross-key rules run only on a tree the schema accepted.
// built adds what this build doesn't support yet to the problems.
func parse(data []byte, built bool) (*Config, *problems) {
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
	validate(ps, root, cfg, built)
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
	case errors.Is(err, fs.ErrNotExist):
		ps.add(path, fmt.Sprintf("%s not found (paths are relative to the config file)", rel))
	case err != nil:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		ps.add(path, fmt.Sprintf("%s can't be read: %v", rel, err))
	case cfg.System.Manifests != "" && !st.IsDir():
		ps.add(path, fmt.Sprintf("%s is not a directory; manifests names a directory of rendered manifests", rel))
	case cfg.System.Compose != "" && st.IsDir():
		ps.add(path, fmt.Sprintf("%s is a directory; for rendered manifests write system: { manifests: %s }", rel, rel))
	}
}
