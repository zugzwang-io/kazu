package config_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/zugzwang-io/kazu/config"
)

// These tests read kazu.schema.json the way an editor or another tool
// would: plain YAML, a generic JSON Schema validator, nothing from Kazu.

func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(config.Schema))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("kazu.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	return c.MustCompile("kazu.schema.json")
}

func validateYAML(t *testing.T, sch *jsonschema.Schema, src []byte) error {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}
	return sch.Validate(v)
}

// The configs in DESIGN.md and the README are valid.
func TestSchemaAcceptsDocumentedConfigs(t *testing.T) {
	sch := compile(t)
	for _, file := range []string{"testdata/reference.yaml", "testdata/walkthrough.yaml"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateYAML(t, sch, src); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(readme), "```yaml\n")
	example, _, ok2 := strings.Cut(rest, "```")
	if !ok || !ok2 {
		t.Fatal("README.md has no yaml example")
	}
	if err := validateYAML(t, sch, []byte(example)); err != nil {
		t.Errorf("README.md: %v", err)
	}
}

// What the schema rejects on its own. Rules relating one key to another
// (a fault's edge must be declared, ...) are Kazu's, not the schema's.
func TestSchemaRejects(t *testing.T) {
	const head = "system: docker-compose.yml\ntraffic: { image: grafana/k6:1.0.0 }\n"
	tests := []struct {
		name, yaml string
		at         string // where the error says it is
	}{
		{"missing traffic", "system: docker-compose.yml\n", "at ''"},
		{"unknown top-level key", head + "scenarioz: {}\n", "at ''"},
		{"traffic without an image", "system: x\ntraffic: { command: [run] }\n", "at '/traffic'"},
		{"command as a string", head + "scenarios: { a: { traffic: { command: run /x.js } } }\n", "at '/scenarios/a/traffic/command'"},
		// Not a location: v6.0.3 reports propertyNames errors at a
		// location that can belong to a sibling key.
		{"edge name without an arrow", head + "edges: { checkout payments: tcp }\n", "invalid propertyName 'checkout payments'"},
		{"unknown protocol", head + "edges: { a -> b: udp }\n", "at '/edges/a -> b'"},
		{"tls without a key", head + "edges: { a -> b: { protocol: http, tls: { ca_cert: x } } }\n", "at '/edges/a -> b/tls'"},
		{"unknown fault kind", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: slow } ] } }\n", "at '/scenarios/a/faults/0/fault'"},
		{"latency without delay", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: latency } ] } }\n", "at '/scenarios/a/faults/0'"},
		{"errors without code", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: errors, rate: 1% } ] } }\n", "at '/scenarios/a/faults/0'"},
		{"duration without a unit", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: down, after: 20 } ] } }\n", "at '/scenarios/a/faults/0/after'"},
		{"rate without %", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: reset, rate: 0.1 } ] } }\n", "at '/scenarios/a/faults/0/rate'"},
		{"zero request count", head + "scenarios: { a: { faults: [ { edge: a -> b, fault: down, after_requests: 0 } ] } }\n", "at '/scenarios/a/faults/0/after_requests'"},
		{"check with an unknown key", head + "checks: [ { sql: { on: pg, query: x, expect: empty }, retries: 3 } ]\n", "at '/checks/0'"},
		{"unknown measure", head + "checks: [ { no_regression: { service: a, measure: p100, within: 1% } } ]\n", "at '/checks/0/no_regression/measure'"},
		{"empty suite", head + "suites: { pr: [] }\n", "at '/suites/pr'"},
		{"suite as a word other than all", head + "suites: { pr: everything }\n", "at '/suites/pr'"},
		{"trials as a word other than auto", head + "gate: { trials: lots }\n", "at '/gate/trials'"},
		{"unknown severity", head + "gate: { flaky: ignore }\n", "at '/gate/flaky'"},
	}
	sch := compile(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateYAML(t, sch, []byte(tt.yaml))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tt.at) {
				t.Errorf("error doesn't name %s:\n%v", tt.at, err)
			}
		})
	}
}
