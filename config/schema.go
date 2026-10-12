package config

import _ "embed"

// Schema is the published JSON schema for kazu.yaml, for editors and for
// anyone generating configs.
//
//go:embed kazu.schema.json
var Schema []byte
