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

const schemaID = "https://zugzwang-io.github.io/kazu/kazu.schema.json"

func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(config.Schema))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaID, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		t.Fatal(err)
	}
	return sch
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
	examples := yamlBlocks(string(readme))
	if len(examples) == 0 {
		t.Fatal("README.md has no yaml example")
	}
	for i, example := range examples {
		if err := validateYAML(t, sch, []byte(example)); err != nil {
			t.Errorf("README.md yaml block %d: %v", i, err)
		}
	}
}

func yamlBlocks(markdown string) []string {
	var blocks []string
	for {
		_, rest, ok := strings.Cut(markdown, "```yaml\n")
		if !ok {
			return blocks
		}
		block, after, ok := strings.Cut(rest, "```")
		if !ok {
			return blocks
		}
		blocks = append(blocks, block)
		markdown = after
	}
}

const head = "system: docker-compose.yml\ntraffic: { image: grafana/k6:1.0.0 }\n"

func fault(fields string) string {
	return head + "edges: { a -> b: http }\nscenarios: { s: { faults: [ { " + fields + " } ] } }\n"
}

func check(entry string) string {
	return head + "checks: [ { " + entry + " } ]\n"
}

// Every shape the schema promises to accept, including the ADR-0002
// proposals the documented configs don't happen to use.
func TestSchemaAccepts(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"system as manifests", "system: { manifests: ./k8s/rendered }\ntraffic: { image: x }\n"},
		{"environment as a list", head + "scenarios: { s: { traffic: { environment: [FLOW=browse, RATE=50, EMPTY=] } } }\n"},
		{"environment scalars", head + "scenarios: { s: { env: { traffic: { RATE: 50, DEBUG: true, FLOW: browse } } } }\n"},
		{"empty scenario", head + "scenarios: { baseline: }\n"},
		{"empty scenario map", head + "scenarios: { baseline: {} }\n"},
		{"scenario names with dots, underscores and dashes", head + "scenarios: { v1.2_rc-3: {} }\n"},
		{"edge without spaces", head + "edges: { checkout->payments: http }\n"},
		{"edge with tls", head + "edges: { a -> b: { protocol: grpc, tls: { ca_cert: ./ca.pem, ca_key: '${env:KEY}' } } }\n"},
		{"suite of auto scenarios", head + "suites: { pr: [auto] }\n"},
		{"scenario checks", head + "scenarios: { s: { checks: [ { recovers: { service: a, within: 30s } }, { image: x, volumes: [./c:/c] } ] } }\n"},

		{"latency", fault("edge: a -> b, fault: latency, delay: 300ms, jitter: 50ms, after: 20s")},
		{"down on a service", fault("service: b, fault: down, after: 20s, for: 15s")},
		{"blackhole", fault("edge: a -> b, fault: blackhole, for: 20s")},
		{"reset", fault("edge: a -> b, fault: reset, rate: 2%")},
		{"bandwidth", fault("edge: a -> b, fault: bandwidth, limit: 64kbps")},
		{"errors with an http status", fault("edge: a -> b, fault: errors, rate: 10%, code: 503, after: 20s")},
		{"errors with a grpc code", fault("edge: a -> b, fault: errors, rate: 1%, code: UNAVAILABLE")},
		{"timeout", fault("edge: a -> b, fault: timeout, rate: 5%, after: 1m")},
		{"crash", fault("service: b, fault: crash, after_requests: 200, restart_after: 5s")},
		{"crash without restart_after", fault("service: b, fault: crash, after: 20s")},
		{"pause", fault("service: b, fault: pause, for: 10s")},
		{"cpu", fault("service: b, fault: cpu, load: 90%")},
		{"memory", fault("service: b, fault: memory, load: 100%")},
		{"disk_full", fault("service: b, fault: disk_full, after: 30s")},
		{"named fault", fault("name: slow-pay, edge: a -> b, fault: latency, delay: 300ms, for: 30s")},
		{"from a fault's end", fault("edge: a -> b, fault: down, from: slow-pay, after: 5s")},
		{"from a fault's start", fault("edge: a -> b, fault: down, from: { start: slow-pay }")},
		{"from the latest of several", fault("edge: a -> b, fault: down, from: [slow-pay, { start: db-down }], after: 5s")},

		{"compound durations", fault("edge: a -> b, fault: latency, delay: 1.5s, after: 1m30s, for: 2h")},
		{"fractional share", fault("edge: a -> b, fault: reset, rate: 0.5%")},
		{"bandwidth units", head + "scenarios: { s: { faults: [ { edge: a -> b, fault: bandwidth, limit: 100bps }, { edge: a -> b, fault: bandwidth, limit: 1.5mbps }, { edge: a -> b, fault: bandwidth, limit: 2gbps } ] } }\n"},
		{"tolerances over 100%", check("no_regression: { service: a, measure: p99, within: 200% }")},

		{"image check with compose keys", check("image: ghcr.io/zugzwang-io/kazu-python:1, command: [python, -m, checks], volumes: [./checks:/checks:ro], environment: { DEBUG: '1' }")},
		{"sql check expecting a value", check("name: one-charge, sql: { on: pg, query: select count(*) from charges, expect: 1 }")},
		{"http check", check("http: { on: api, method: POST, path: /orders, expect: { status: 201, body: ord_ } }")},
		{"http check with default method", check("http: { on: api, path: /healthz, expect: { status: 200 } }")},
		{"recovers", check("recovers: { service: payments, within: 30s }")},
		{"slo on latency", check("slo: { service: checkout, measure: p99, max: 250ms }")},
		{"slo on error rate", check("slo: { service: checkout, measure: error_rate, max: 1% }")},
		{"metric with max", check("metric: { query: up, max: 3 }")},
		{"metric with min and max", check("metric: { query: up, min: 1, max: 3 }")},
		{"metric within base with no_data", check("metric: { query: up, within: 50%, no_data: warn }")},
	}
	sch := compile(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateYAML(t, sch, []byte(tt.yaml)); err != nil {
				t.Errorf("rejected:\n%v", err)
			}
		})
	}
}

// What the schema rejects on its own. Rules relating one key to another
// (a fault's edge must be declared, ...) are Kazu's, not the schema's.
func TestSchemaRejects(t *testing.T) {
	tests := []struct {
		name, yaml string
		at         string // the instance path the error names; "*" when v6.0.3 misplaces it
		want       string // a fragment of its message; "" for any
	}{
		{"missing system", "traffic: { image: x }\n", "", "missing property 'system'"},
		{"missing traffic", "system: docker-compose.yml\n", "", "missing property 'traffic'"},
		{"empty system", "system: ''\ntraffic: { image: x }\n", "/system", ""},
		{"system as an empty map", "system: {}\ntraffic: { image: x }\n", "/system", "missing property 'manifests'"},
		{"system as a number", "system: 5\ntraffic: { image: x }\n", "/system", ""},
		{"unknown top-level key", head + "scenarioz: {}\n", "", "additional properties 'scenarioz' not allowed"},
		{"traffic without an image", "system: x\ntraffic: { command: [run] }\n", "/traffic", "missing property 'image'"},
		{"command as a string", head + "scenarios: { a: { traffic: { command: run /x.js } } }\n", "/scenarios/a/traffic/command", "want array"},
		{"command with a number", head + "scenarios: { a: { traffic: { command: [run, 5] } } }\n", "/scenarios/a/traffic/command/1", "want string"},
		{"environment list entry without =", head + "scenarios: { a: { traffic: { environment: [FLOW] } } }\n", "/scenarios/a/traffic/environment/0", "does not match pattern"},
		{"environment value as a list", head + "scenarios: { a: { traffic: { environment: { FLOW: [a] } } } }\n", "/scenarios/a/traffic/environment/FLOW", ""},
		// Not a location: v6.0.3 reports propertyNames errors at a
		// location that can belong to a sibling key.
		{"edge name without an arrow", head + "edges: { checkout payments: tcp }\n", "*", "invalid propertyName 'checkout payments'"},
		{"scenario name with a space", head + "scenarios: { bad name: {} }\n", "*", "invalid propertyName 'bad name'"},
		{"scenario name starting with a dash", head + "scenarios: { -a: {} }\n", "*", "invalid propertyName '-a'"},
		{"unknown protocol", head + "edges: { a -> b: udp }\n", "/edges/a -> b", ""},
		{"tls without a key", head + "edges: { a -> b: { protocol: http, tls: { ca_cert: x } } }\n", "/edges/a -> b/tls", "missing property 'ca_key'"},
		{"telemetry, not in the schema yet", head + "telemetry: { collector: otel }\n", "", "additional properties 'telemetry' not allowed"},
		{"auto_scenarios, not in the schema yet", head + "auto_scenarios: true\n", "", "additional properties 'auto_scenarios' not allowed"},
		{"defaults, not in the schema yet", head + "defaults: { off: [latency] }\n", "", "additional properties 'defaults' not allowed"},
		{"gate, not in the schema yet", head + "gate: { trials: 3 }\n", "", "additional properties 'gate' not allowed"},

		{"fault without a kind", fault("edge: a -> b, after: 20s"), "/scenarios/s/faults/0", "missing property 'fault'"},
		{"unknown fault kind", fault("edge: a -> b, fault: slow"), "/scenarios/s/faults/0/fault", ""},
		{"fault without a target", fault("fault: down, after: 20s"), "/scenarios/s/faults/0", "'oneOf' failed"},
		{"fault with both targets", fault("edge: a -> b, service: b, fault: down"), "/scenarios/s/faults/0", "'oneOf' failed"},
		{"both triggers", fault("edge: a -> b, fault: down, after: 20s, after_requests: 10"), "/scenarios/s/faults/0", "'not' failed"},
		{"latency without delay", fault("edge: a -> b, fault: latency"), "/scenarios/s/faults/0", "missing property 'delay'"},
		{"reset without rate", fault("edge: a -> b, fault: reset"), "/scenarios/s/faults/0", "missing property 'rate'"},
		{"timeout without rate", fault("edge: a -> b, fault: timeout"), "/scenarios/s/faults/0", "missing property 'rate'"},
		{"errors without rate", fault("edge: a -> b, fault: errors, code: 503"), "/scenarios/s/faults/0", "missing property 'rate'"},
		{"errors without code", fault("edge: a -> b, fault: errors, rate: 1%"), "/scenarios/s/faults/0", "missing property 'code'"},
		{"bandwidth without limit", fault("edge: a -> b, fault: bandwidth"), "/scenarios/s/faults/0", "missing property 'limit'"},
		{"cpu without load", fault("service: b, fault: cpu"), "/scenarios/s/faults/0", "missing property 'load'"},
		{"memory without load", fault("service: b, fault: memory"), "/scenarios/s/faults/0", "missing property 'load'"},
		{"a field from another kind", fault("edge: a -> b, fault: down, delay: 300ms"), "/scenarios/s/faults/0/delay", "false schema"},
		{"rate on latency", fault("edge: a -> b, fault: latency, delay: 300ms, rate: 50%"), "/scenarios/s/faults/0/rate", "false schema"},
		{"restart_after on down", fault("service: b, fault: down, restart_after: 5s"), "/scenarios/s/faults/0/restart_after", "false schema"},
		{"errors on a service", fault("service: b, fault: errors, rate: 1%, code: 503"), "/scenarios/s/faults/0/service", "false schema"},
		{"timeout on a service", fault("service: b, fault: timeout, rate: 1%"), "/scenarios/s/faults/0/service", "false schema"},
		{"crash on an edge", fault("edge: a -> b, fault: crash, restart_after: 5s"), "/scenarios/s/faults/0/edge", "false schema"},
		{"cpu on an edge", fault("edge: a -> b, fault: cpu, load: 90%"), "/scenarios/s/faults/0/edge", "false schema"},
		{"unknown fault key", fault("edge: a -> b, fault: down, retries: 3"), "/scenarios/s/faults/0", "additional properties 'retries' not allowed"},
		{"fault name with a space", fault("name: slow pay, edge: a -> b, fault: down"), "/scenarios/s/faults/0/name", "does not match pattern"},
		{"from with after_requests", fault("edge: a -> b, fault: down, from: x, after_requests: 10"), "/scenarios/s/faults/0/after_requests", "false schema"},
		{"from as an empty list", fault("edge: a -> b, fault: down, from: []"), "/scenarios/s/faults/0/from", "minItems"},
		{"from naming the same anchor twice", fault("edge: a -> b, fault: down, from: [x, x]"), "/scenarios/s/faults/0/from", "are equal"},
		{"from a fault's end, spelled out", fault("edge: a -> b, fault: down, from: { end: x }"), "/scenarios/s/faults/0/from", "missing property 'start'"},
		{"from a name with a space", fault("edge: a -> b, fault: down, from: slow pay"), "/scenarios/s/faults/0/from", "does not match pattern"},
		{"from as a number", fault("edge: a -> b, fault: down, from: 5"), "/scenarios/s/faults/0/from", ""},

		{"duration without a unit", fault("edge: a -> b, fault: down, after: 20"), "/scenarios/s/faults/0/after", "want string"},
		{"duration as quoted number", fault("edge: a -> b, fault: down, after: '20'"), "/scenarios/s/faults/0/after", "does not match pattern"},
		{"duration with a space", fault("edge: a -> b, fault: down, after: 1m 30s"), "/scenarios/s/faults/0/after", "does not match pattern"},
		{"negative duration", fault("edge: a -> b, fault: down, after: -5s"), "/scenarios/s/faults/0/after", "does not match pattern"},
		{"duration in microseconds", fault("edge: a -> b, fault: latency, delay: 5us"), "/scenarios/s/faults/0/delay", "does not match pattern"},
		{"rate without %", fault("edge: a -> b, fault: reset, rate: 0.1"), "/scenarios/s/faults/0/rate", "want string"},
		{"rate over 100%", fault("edge: a -> b, fault: reset, rate: 150%"), "/scenarios/s/faults/0/rate", "does not match pattern"},
		{"load over 100%", fault("service: b, fault: cpu, load: 900%"), "/scenarios/s/faults/0/load", "does not match pattern"},
		{"rate as pct", fault("edge: a -> b, fault: reset, rate: 20pct"), "/scenarios/s/faults/0/rate", "does not match pattern"},
		{"bandwidth without a unit", fault("edge: a -> b, fault: bandwidth, limit: 64"), "/scenarios/s/faults/0/limit", "want string"},
		{"bandwidth in bytes", fault("edge: a -> b, fault: bandwidth, limit: 64kb"), "/scenarios/s/faults/0/limit", "does not match pattern"},
		{"bandwidth with a capital", fault("edge: a -> b, fault: bandwidth, limit: 64Kbps"), "/scenarios/s/faults/0/limit", "does not match pattern"},
		{"empty grpc code", fault("edge: a -> b, fault: errors, rate: 1%, code: ''"), "/scenarios/s/faults/0/code", "minLength"},
		{"zero request count", fault("edge: a -> b, fault: down, after_requests: 0"), "/scenarios/s/faults/0/after_requests", "minimum"},
		{"request count as text", fault("edge: a -> b, fault: down, after_requests: '200'"), "/scenarios/s/faults/0/after_requests", "want integer"},

		{"check with nothing in it", check(""), "/checks/0", "'oneOf' failed"},
		{"check with only a name", check("name: x"), "/checks/0", "'oneOf' failed"},
		{"check with two kinds", check("sql: { on: pg, query: x, expect: empty }, slo: { service: a, measure: p99, max: 1s }"), "/checks/0", "'oneOf' failed"},
		{"volumes without an image", check("volumes: [./checks:/checks:ro]"), "/checks/0", "'image'"},
		{"command without an image", check("sql: { on: pg, query: x, expect: empty }, command: [x]"), "/checks/0", "'image'"},
		{"check with an unknown key", check("sql: { on: pg, query: x, expect: empty }, retries: 3"), "/checks/0", "additional properties 'retries' not allowed"},
		{"check name with a space", check("name: no double charge, sql: { on: pg, query: x, expect: empty }"), "/checks/0/name", "does not match pattern"},
		{"sql without expect", check("sql: { on: pg, query: x }"), "/checks/0/sql", "missing property 'expect'"},
		{"http without on", check("http: { path: /x, expect: { status: 200 } }"), "/checks/0/http", "missing property 'on'"},
		{"http path without a slash", check("http: { on: api, path: x, expect: { status: 200 } }"), "/checks/0/http/path", "does not match pattern"},
		{"http with an unknown method", check("http: { on: api, method: OPTIONS, path: /x, expect: { status: 200 } }"), "/checks/0/http/method", ""},
		{"http expecting nothing", check("http: { on: api, path: /x, expect: {} }"), "/checks/0/http/expect", "minProperties"},
		{"http status out of range", check("http: { on: api, path: /x, expect: { status: 600 } }"), "/checks/0/http/expect/status", "maximum"},
		{"unknown measure", check("no_regression: { service: a, measure: p100, within: 1% }"), "/checks/0/no_regression/measure", ""},
		{"slo max as a word", check("slo: { service: a, measure: p99, max: fast }"), "/checks/0/slo/max", "does not match pattern"},
		{"slo latency as a percent", check("slo: { service: a, measure: p99, max: 1% }"), "/checks/0/slo/max", "does not match pattern"},
		{"slo error rate as a duration", check("slo: { service: a, measure: error_rate, max: 250ms }"), "/checks/0/slo/max", "does not match pattern"},
		{"metric without a query", check("metric: { max: 3 }"), "/checks/0/metric", "missing property 'query'"},
		{"metric without a bound", check("metric: { query: up }"), "/checks/0/metric", "'anyOf' failed"},
		{"metric with within and max", check("metric: { query: up, within: 5%, max: 3 }"), "/checks/0/metric/max", "false schema"},
		{"metric max as text", check("metric: { query: up, max: '3' }"), "/checks/0/metric/max", "want number"},
		{"metric within in points", check("metric: { query: up, within: 1pp }"), "/checks/0/metric/within", "does not match pattern"},
		{"unknown severity", check("metric: { query: up, max: 3, no_data: ignore }"), "/checks/0/metric/no_data", ""},
		{"scenario check with two kinds", head + "scenarios: { s: { checks: [ { image: x, sql: { on: pg, query: x, expect: empty } } ] } }\n", "/scenarios/s/checks/0", "'oneOf' failed"},

		{"empty suite", head + "suites: { pr: [] }\n", "/suites/pr", "minItems"},
		{"suite as a word other than all", head + "suites: { pr: everything }\n", "/suites/pr", ""},
	}
	sch := compile(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateYAML(t, sch, []byte(tt.yaml))
			if err == nil {
				t.Fatal("accepted")
			}
			msg := err.Error()
			if tt.at != "*" && !strings.Contains(msg, "at '"+tt.at+"':") {
				t.Errorf("no error at %q in:\n%v", tt.at, err)
			}
			if !strings.Contains(msg, tt.want) {
				t.Errorf("no error saying %q in:\n%v", tt.want, err)
			}
		})
	}
}
