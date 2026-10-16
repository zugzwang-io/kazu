// Package config reads kazu.yaml: it checks the file against the published
// JSON schema (kazu.schema.json) and decodes it into Config. Every problem
// is reported with its file, line and column, all at once rather than one
// per run.
//
// Config holds declarations only. Nothing here knows about the compose file,
// images or runners; the driver checks those when it starts the system.
package config
