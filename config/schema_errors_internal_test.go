package config

import (
	"slices"
	"strconv"
	"testing"
)

// worded lists every keyword the schema may use: those whose failures
// leafMessage or schemaProblems put in words, those that only hold other
// schemas, and annotations. A keyword outside it would reach the user in
// the validator's own wording, so using one means wording it first.
var worded = []string{
	"type", "enum", "const", "pattern", "minimum", "maximum", "uniqueItems",
	"minItems", "minLength", "minProperties", "required", "dependentRequired",
	"oneOf", "anyOf", "not", "propertyNames", "additionalProperties",
	"properties", "items", "allOf", "if", "then", "else", "$ref", "$defs", "dependentSchemas",
	"$schema", "$id", "title", "description",
}

func TestSchemaUsesOnlyWordedKeywords(t *testing.T) {
	compiledSchema()
	walkSchema(schemaDoc, "#", func(at string, s map[string]any) {
		for k := range s {
			if !slices.Contains(worded, k) {
				t.Errorf("%s uses %q, which has no wording in schema_errors.go", at, k)
			}
		}
	})
}

func TestEverySchemaPatternHasAName(t *testing.T) {
	compiledSchema()
	walkSchema(schemaDoc, "#", func(at string, s map[string]any) {
		if p, ok := s["pattern"].(string); ok {
			if _, named := patternNames[p]; !named {
				t.Errorf("%s: pattern %q has no entry in patternNames, so errors would show the regex", at, p)
			}
		}
	})
}

// walkSchema calls visit on every schema object in v, following the
// keywords that hold subschemas.
func walkSchema(v any, at string, visit func(at string, s map[string]any)) {
	s, ok := v.(map[string]any)
	if !ok {
		return // a boolean schema
	}
	visit(at, s)
	for _, k := range []string{"properties", "$defs", "dependentSchemas"} {
		m, _ := s[k].(map[string]any)
		for name, sub := range m {
			walkSchema(sub, at+"/"+k+"/"+name, visit)
		}
	}
	for _, k := range []string{"items", "additionalProperties", "propertyNames", "not", "if", "then", "else"} {
		if sub, ok := s[k]; ok {
			walkSchema(sub, at+"/"+k, visit)
		}
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		list, _ := s[k].([]any)
		for i, sub := range list {
			walkSchema(sub, at+"/"+k+"/"+strconv.Itoa(i), visit)
		}
	}
}
