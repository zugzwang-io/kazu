// Package config reads kazu.yaml. Its structure is published as a JSON
// schema, kazu.schema.json, embedded here as Schema for editors and tools,
// and Config is the same structure in Go.
//
// Config holds declarations only. Nothing here knows about the compose file,
// images or runners; the driver checks those when it starts the system.
package config
