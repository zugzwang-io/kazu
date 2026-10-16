package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zugzwang-io/kazu/config"
)

func TestSupportedConfigLoads(t *testing.T) {
	if _, err := config.Load("testdata/supported.yaml"); err != nil {
		t.Fatal(err)
	}
}

// Everything the schema allows but this build doesn't do is an error, never
// a silent no-op. Delete a case when its feature is built.
func TestNotSupportedYet(t *testing.T) {
	const head = "system: docker-compose.yml\ntraffic: { image: grafana/k6:1.0.0 }\nedges: { a -> b: tcp, b -> c: tcp }\nconnections: { pg: postgres://pg/shop }\n"
	tests := []struct{ name, yaml string }{
		{"manifests", "system: { manifests: ./k8s }\ntraffic: { image: x }\n"},
		{"http edge", "system: x\ntraffic: { image: x }\nedges: { x -> y: http }\n"},
		{"grpc edge with tls", "system: x\ntraffic: { image: x }\nedges: { x -> y: { protocol: grpc, tls: { ca_cert: a, ca_key: b } } }\n"},
		{"scenario traffic", head + "scenarios: { s: { traffic: { command: [run] } } }\n"},
		{"scenario not_exercised", head + "scenarios: { s: { not_exercised: warn } }\n"},
		{"fault kind", head + "scenarios: { s: { faults: [ { edge: a -> b, fault: blackhole } ] } }\n"},
		{"fault on a service", head + "scenarios: { s: { faults: [ { service: b, fault: down } ] } }\n"},
		{"from", head + "scenarios: { s: { faults: [ { name: x, edge: a -> b, fault: down, for: 5s }, { edge: b -> c, fault: down, from: x } ] } }\n"},
		{"check kind", head + "checks: [ { metric: { query: up, max: 1 } } ]\n"},
		{"scenario check kind", head + "scenarios: { s: { checks: [ { metric: { query: up, max: 1 } } ] } }\n"},
		{"generated scenarios", head + "suites: { pr: [auto] }\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.ParseContract("kazu.yaml", []byte(tt.yaml)); err != nil {
				t.Fatalf("the contract rejects it, so this case tests nothing: %v", err)
			}
			_, err := config.Parse("kazu.yaml", []byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), "not supported yet") {
				t.Errorf("got %v", err)
			}
		})
	}
}

// The messages say what is unsupported and what to use instead.
func TestNotSupportedYetMessages(t *testing.T) {
	_, err := config.Parse("kazu.yaml", []byte(`system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  a -> b: http
  b -> c: { protocol: grpc }
scenarios:
  s:
    not_exercised: warn
    traffic: { command: [run] }
    faults:
      - { service: c, fault: crash, after_requests: 3 }
      - { name: x, edge: a -> b, fault: errors, rate: 5%, code: 503, for: 5s }
      - { edge: a -> b, fault: down, from: x }
checks:
  - recovers: { service: b, within: 5s }
suites:
  pr: [s, auto-crash-b]
`))
	want := []string{
		`4:11: edges["a -> b"]: http edges are not supported yet; declare it tcp, where latency and down work`,
		`5:23: edges["b -> c"].protocol: grpc edges are not supported yet; declare it tcp, where latency and down work`,
		`8:5: scenarios.s.not_exercised: not_exercised is not supported yet; a fault that never lands always makes the scenario not exercised`,
		`9:5: scenarios.s.traffic: a scenario's traffic is not supported yet; vary traffic with env`,
		`11:11: scenarios.s.faults[0].service: faults on a service are not supported yet; put the fault on an edge`,
		`11:30: scenarios.s.faults[0].fault: crash faults are not supported yet; latency and down are`,
		`11:37: scenarios.s.faults[0].after_requests: after_requests is not supported yet; use after`,
		`12:41: scenarios.s.faults[1].fault: errors faults are not supported yet; latency and down are`,
		`13:38: scenarios.s.faults[2].from: from is not supported yet; after counts from traffic start`,
		`15:5: checks[0].recovers: recovers checks are not supported yet; image and sql are`,
		`17:11: suites.pr[1]: generated scenarios are not supported yet`,
	}
	var got []string
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			got = append(got, strings.TrimPrefix(line, "kazu.yaml:"))
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
