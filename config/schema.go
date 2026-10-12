package config

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema is the published JSON schema for kazu.yaml, for editors and for
// anyone generating configs.
//
//go:embed kazu.schema.json
var Schema []byte

const schemaURL = "https://zugzwang-io.github.io/kazu/kazu.schema.json"

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
