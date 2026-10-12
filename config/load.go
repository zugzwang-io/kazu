package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse validates a config held in memory. file names it in errors.
func Parse(file string, data []byte) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: the file is empty; it needs at least system and traffic", file)
	}
	f := flattener{}
	root, err := f.flatten(doc.Content[0], 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if err := compiledSchema().Validate(toAny(root)); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	d := decoder{}
	return d.config(root), nil
}
