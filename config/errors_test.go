package config_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"gopkg.in/yaml.v3"

	"github.com/zugzwang-io/kazu/config"
)

// Golden messages, appended to base so a case's first line is line 10.
func TestProblems(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown top-level key", "scenarioz: {}\n", []string{
			`10:1: scenarioz: unknown key "scenarioz" (did you mean "scenarios"?)`,
		}},
		{"wrong type", "scenarios: 5\n", []string{
			`10:12: scenarios: expected a map, got a number`,
		}},
		{"command as text", "scenarios: { a: { traffic: { command: run /x.js } } }\n", []string{
			`10:39: scenarios.a.traffic.command: expected a list, got text`,
		}},
		{"unknown fault key", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency, delay: 1s, dealy: 2s }\n", []string{
			`13:66: scenarios.a.faults[0].dealy: unknown key "dealy" (did you mean "delay"?)`,
		}},
		{"unknown fault kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latncy, delay: 1s }\n", []string{
			`13:46: scenarios.a.faults[0].fault: "latncy" is not one of "latency", "down", "blackhole", "reset", "bandwidth", "errors", "timeout", "crash", "pause", "cpu", "memory", "disk_full" (did you mean "latency"?)`,
		}},
		{"fault without a kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, after: 20s }\n", []string{
			`13:9: scenarios.a.faults[0]: missing key "fault"`,
		}},
		{"missing required field", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency }\n", []string{
			`13:9: scenarios.a.faults[0]: missing key "delay"`,
		}},
		{"errors needs rate and code", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: errors }\n", []string{
			`13:9: scenarios.a.faults[0]: missing key "rate", "code"`,
		}},
		{"fault without a target", "scenarios:\n  a:\n    faults:\n      - { fault: down, after: 20s }\n", []string{
			`13:9: scenarios.a.faults[0]: needs one of edge or service`,
		}},
		{"fault with both targets", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, service: payments, fault: down }\n", []string{
			`13:9: scenarios.a.faults[0]: has edge and service; keep one`,
		}},
		{"both triggers", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20s, after_requests: 5 }\n", []string{
			`13:9: scenarios.a.faults[0]: can't have both after and after_requests`,
		}},
		{"field of another kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, delay: 1s }\n", []string{
			`13:59: scenarios.a.faults[0].delay: "delay" is not a field of a down fault`,
		}},
		{"process fault on an edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: crash }\n", []string{
			`13:17: scenarios.a.faults[0].edge: "edge" is not a field of a crash fault`,
		}},
		{"zero request count", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after_requests: 0 }\n", []string{
			`13:68: scenarios.a.faults[0].after_requests: must be at least 1`,
		}},
		{"duration without a unit", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20 }\n", []string{
			`13:59: scenarios.a.faults[0].after: expected a duration such as 300ms, 20s or 1m30s, got a number`,
		}},
		{"rate over 100%", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: reset, rate: 150% }\n", []string{
			`13:59: scenarios.a.faults[0].rate: "150%" is not a percentage from 0% to 100%`,
		}},
		{"check without a kind", "checks:\n  - { name: x }\n", []string{
			`11:5: checks[0]: needs one of image, sql, http, recovers, no_regression, slo or metric`,
		}},
		{"check with two kinds", "checks:\n  - { sql: { on: postgres, query: x }, recovers: { service: payments, within: 1s } }\n", []string{
			`11:5: checks[0]: has sql and recovers; keep one`,
		}},
		{"volumes without an image", "checks:\n  - { volumes: [./x:/x] }\n", []string{
			`11:5: checks[0]: "volumes" needs "image"`,
			`11:5: checks[0]: needs one of image, sql, http, recovers, no_regression, slo or metric`,
		}},
		{"metric without a bound", "checks:\n  - { metric: { query: up } }\n", []string{
			`11:15: checks[0].metric: needs at least one of max, min or within`,
		}},
		{"metric with within and max", "checks:\n  - { metric: { query: up, within: 5%, max: 3 } }\n", []string{
			`11:45: checks[0].metric.max: "max" can't go with "within"`,
		}},
		{"slo latency as a percent", "checks:\n  - { slo: { service: checkout, measure: p99, max: 1% } }\n", []string{
			`11:52: checks[0].slo.max: "1%" is not a duration such as 300ms, 20s or 1m30s`,
		}},
		{"empty suite", "suites:\n  pr: []\n", []string{
			`11:7: suites.pr: must not be empty`,
		}},
		{"word other than all, severity other than fail or warn", "suites: { pr: everything }\nscenarios: { a: { not_exercised: maybe } }\n", []string{
			`10:15: suites.pr: "everything" is not allowed here; the only text value is "all"`,
			`11:34: scenarios.a.not_exercised: "maybe" is not one of "fail", "warn"`,
		}},
		{"from with after_requests", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, from: x, after_requests: 5 }\n", []string{
			`13:77: scenarios.a.faults[0].after_requests: "after_requests" can't go with "from"`,
		}},
		{"from a fault's end, spelled out", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, from: { end: x } }\n", []string{
			`13:58: scenarios.a.faults[0].from: missing key "start"`,
			`13:60: scenarios.a.faults[0].from.end: unknown key "end"`,
		}},
		{"from as an empty list", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, from: [] }\n", []string{
			`13:58: scenarios.a.faults[0].from: must not be empty`,
		}},
		{"from a name with a space", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, from: [ok, slow pay] }\n", []string{
			`13:63: scenarios.a.faults[0].from[1]: "slow pay" is not a name (letters, digits, '.', '_' and '-')`,
		}},
		{"key that is not a name", "scenarios:\n  a: { faults: [] }\n  b:\n    env:\n      checkout payments: { A: b }\n", []string{
			`14:7: scenarios.b.env["checkout payments"]: "checkout payments" is not a name (letters, digits, '.', '_' and '-')`,
		}},
		{"same bad key in two maps", "scenarios:\n  a: { env: { checkout: { BAD-VAR: x } } }\n  b: { env: { checkout: { BAD-VAR: x } } }\n", []string{
			`11:27: scenarios.a.env.checkout.BAD-VAR: "BAD-VAR" is not an env var name`,
			`12:27: scenarios.b.env.checkout.BAD-VAR: "BAD-VAR" is not an env var name`,
		}},
		{"problems on several lines sort by line", "suites: { pr: everything }\nscenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency }\n      - { fault: down, after: 20 }\n", []string{
			`10:15: suites.pr: "everything" is not allowed here; the only text value is "all"`,
			`14:9: scenarios.a.faults[0]: missing key "delay"`,
			`15:9: scenarios.a.faults[1]: needs one of edge or service`,
			`15:31: scenarios.a.faults[1].after: expected a duration such as 300ms, 20s or 1m30s, got a number`,
		}},
		{"invalid YAML", "x: [\n", []string{
			`10: invalid YAML: did not find expected node content`,
		}},
		{"duplicate key", "scenarios:\n  a: {}\n  a: {}\n", []string{
			`12:3: key "a" appears twice in the same map`,
		}},
		{"duplicate key and a schema problem together", "scenarios:\n  a: {}\n  a: {}\nscenarioz: {}\n", []string{
			`12:3: key "a" appears twice in the same map`,
			`13:1: scenarioz: unknown key "scenarioz" (did you mean "scenarios"?)`,
		}},
		{"duplicate key under an anchor reported once", "scenarios:\n  a: &x { faults: [], faults: [] }\n  b: *x\n  c: *x\n", []string{
			`11:23: key "faults" appears twice in the same map`,
		}},
		{"merge key that is not a map", "scenarios:\n  a: { <<: 5 }\n", []string{
			`11:12: a merge key (<<) takes a map or a list of maps`,
		}},
		{"number the decoder can't hold", "checks: [ { http: { on: postgres, path: /x, expect: { status: 200.0 } } } ]\n", []string{
			`10:63: expected a whole number, got 200.0`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("kazu.yaml", []byte(base+tt.yaml))
			var got []string
			if err != nil {
				for _, line := range strings.Split(err.Error(), "\n") {
					got = append(got, strings.TrimPrefix(line, "kazu.yaml:"))
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// propertyNames problems land on the key that is wrong, in the map that
// owns it, whatever location the validator reports (see the canary below).
func TestPropertyNamePlacement(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"edge key without an arrow", head + "edges:\n  checkout payments: tcp\n",
			`kazu.yaml:4:3: edges["checkout payments"]: "checkout payments" is not an edge written as "caller -> callee"`},
		{"same key valid in connections, invalid in edges", head + "connections:\n  checkout: http://checkout:8080\nedges:\n  checkout: http\n",
			`kazu.yaml:6:3: edges.checkout: "checkout" is not an edge written as "caller -> callee"`},
		{"same key invalid in edges, valid as a scenario", head + "scenarios:\n  nope: {}\nedges:\n  nope: http\n",
			`kazu.yaml:6:3: edges.nope: "nope" is not an edge written as "caller -> callee"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("kazu.yaml", []byte(tt.src))
			if err == nil || err.Error() != tt.want {
				t.Errorf("got  %v\nwant %s", err, tt.want)
			}
		})
	}
}

func TestWholeFileProblems(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"empty", "", "kazu.yaml: the file is empty; it needs at least system and traffic"},
		{"missing traffic", "system: x\n", `kazu.yaml:1:1: missing key "traffic"`},
		{"scalar root", "hello\n", "kazu.yaml:1:1: the file must be a map of keys such as system, traffic and edges"},
		{"list root", "- a\n", "kazu.yaml:1:1: the file must be a map of keys such as system, traffic and edges"},
		{"two documents", "system: x\ntraffic: { image: x }\n---\nsystem: y\n", "kazu.yaml: the file has more than one YAML document; kazu.yaml is one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("kazu.yaml", []byte(tt.src))
			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v\nwant %s", err, tt.want)
			}
		})
	}
}

// The structured error carries what the text shows.
func TestErrorFields(t *testing.T) {
	_, err := config.Parse("kazu.yaml", []byte(base+"scenarios: { a: { not_exercised: maybe } }\n"))
	var e *config.Error
	if !errors.As(err, &e) {
		t.Fatalf("not a *config.Error: %T", err)
	}
	want := config.Problem{Line: 10, Column: 34, Path: "scenarios.a.not_exercised", Message: `"maybe" is not one of "fail", "warn"`}
	if e.File != "kazu.yaml" || len(e.Problems) != 1 || e.Problems[0] != want {
		t.Errorf("error = %+v", e)
	}
}

func TestSuggestThreshold(t *testing.T) {
	for _, key := range []string{"zzzzzzzz", "sys"} {
		_, err := config.Parse("kazu.yaml", []byte(base+key+": {}\n"))
		if err == nil || strings.Contains(err.Error(), "did you mean") {
			t.Errorf("%q: unwanted suggestion in %v", key, err)
		}
	}
}

// Canary for the jsonschema v6.0.3 bug schema_errors.go works around: with
// two sibling maps each holding a bad key, at least one propertyNames error
// names a map that doesn't hold its key. When this fails, upstream has fixed
// it: delete mapsWithKey and schemaLocation and use InstanceLocation.
func TestPropertyNamesLocationBug(t *testing.T) {
	sch := compile(t)
	var v any
	src := head + "scenarios: { a: { env: { s1: { BAD-1: x }, s2: { BAD-2: x } } } }\n"
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	var ve *jsonschema.ValidationError
	if !errors.As(sch.Validate(v), &ve) {
		t.Fatal("accepted")
	}
	misplaced := 0
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if k, ok := e.ErrorKind.(*kind.PropertyNames); ok && !holdsKey(v, e.InstanceLocation, k.Property) {
			misplaced++
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if misplaced == 0 {
		t.Fatal("jsonschema now reports propertyNames errors at the right map; drop the workaround in schema_errors.go")
	}
}

// holdsKey reports whether path leads to a map with the key; a corrupted
// path can end anywhere, so anything else counts as not holding it.
func holdsKey(v any, path []string, key string) bool {
	for _, tok := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		v = m[tok]
	}
	m, ok := v.(map[string]any)
	_, has := m[key]
	return ok && has
}
