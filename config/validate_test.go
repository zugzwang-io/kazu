package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zugzwang-io/kazu/config"
)

// Row S3 in examples/shop/SPEC.md: a request-count trigger on a TCP edge
// is a config error before any trial.
func TestRequestCountTriggerOnTCPEdge(t *testing.T) {
	_, err := config.Parse("kazu.yaml", []byte(base+`
scenarios:
  s3:
    faults:
      - { edge: checkout -> postgres, fault: down, after_requests: 100 }
`))
	want := `kazu.yaml:14:68: scenarios.s3.faults[0].after_requests: checkout -> postgres is a tcp edge: ` +
		`its proxy sees bytes, not requests, so there are none to count. Use after: <duration>, ` +
		`or declare the edge http or grpc if it carries those`
	if err == nil || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	var ce *config.Error
	if !errors.As(err, &ce) || len(ce.Problems) != 1 || ce.Problems[0].Line != 14 {
		t.Errorf("want one *config.Error problem on line 14, got %#v", err)
	}
}

func TestRules(t *testing.T) {
	tests := []struct {
		name string
		yaml string // appended to base
		want []string
	}{
		{"undeclared edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payment, fault: latency, delay: 1s }\n", []string{
			`13:17: scenarios.a.faults[0].edge: checkout -> payment is not a declared edge; faults go only on edges listed under edges (did you mean "checkout -> payments"?)`,
		}},
		{"edge spacing is normalised", "scenarios:\n  a:\n    faults:\n      - { edge: checkout->payments, fault: latency, delay: 1s }\n", nil},
		{"field for another kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, delay: 1s }\n", []string{
			`13:52: scenarios.a.faults[0].delay: delay doesn't apply to a down fault`,
		}},
		{"no target", "scenarios:\n  a:\n    faults:\n      - { fault: down }\n", []string{
			`13:9: scenarios.a.faults[0]: missing a target: set edge (one declared edge) or service`,
		}},
		{"both targets", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, service: payments, fault: down }\n", []string{
			`13:9: scenarios.a.faults[0]: set either edge or service, not both`,
		}},
		{"request fault on a service", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: errors, rate: 5%, code: 503 }\n", []string{
			`13:20: scenarios.a.faults[0].service: an errors fault goes on one http or grpc edge; use edge: <caller> -> payments`,
		}},
		{"request fault on a tcp edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> postgres, fault: timeout, rate: 5% }\n", []string{
			`13:17: scenarios.a.faults[0].edge: a timeout fault needs an http or grpc edge; checkout -> postgres is tcp, so the proxy sees bytes, not requests`,
		}},
		{"process fault on an edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: crash }\n", []string{
			`13:17: scenarios.a.faults[0].edge: a crash fault acts on a service's container; use service: payments`,
		}},
		{"fault on traffic", "scenarios:\n  a:\n    faults:\n      - { service: traffic, fault: pause }\n", []string{
			`13:20: scenarios.a.faults[0].service: traffic is the load, not part of the system; faults go on the system's services and edges`,
		}},
		{"network fault on a service with no inbound edges", "scenarios:\n  a:\n    faults:\n      - { service: fraud, fault: down }\n", []string{
			`13:20: scenarios.a.faults[0].service: a down fault on a service applies to its declared inbound edges, and fraud has none; declare an edge into fraud`,
		}},
		{"request count on a service with only tcp edges", "scenarios:\n  a:\n    faults:\n      - { service: postgres, fault: crash, after_requests: 3 }\n", []string{
			`13:60: scenarios.a.faults[0].after_requests: after_requests on a service counts requests on its declared http and grpc inbound edges, and postgres has none. Use after: <duration>`,
		}},
		{"both triggers", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 1s, after_requests: 3 }\n", []string{
			`13:9: scenarios.a.faults[0]: set after or after_requests, not both`,
		}},
		{"for on a crash", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: crash, for: 5s }\n", []string{
			`13:49: scenarios.a.faults[0].for: a crash is instant; restart_after sets how long the service stays down`,
		}},
		{"zero rate", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: reset, rate: 0% }\n", []string{
			`13:59: scenarios.a.faults[0].rate: 0% is outside 0% to 100% (zero would never fire)`,
		}},
		{"zero duration", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, for: 0s }\n", []string{
			`13:57: scenarios.a.faults[0].for: must be longer than zero`,
		}},
		{"gRPC code on an HTTP edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: errors, rate: 5%, code: UNAVAILABLE }\n", []string{
			`13:70: scenarios.a.faults[0].code: "UNAVAILABLE" is not an HTTP status; use a number from 100 to 599, such as 503`,
		}},
		{"reserved scenario names", "scenarios:\n  auto-x: {}\n  all: {}\n", []string{
			`11:3: scenarios.auto-x: "auto-x" is reserved: "all" and "auto" are suite keywords, and names starting with "auto-" belong to generated scenarios`,
			`12:3: scenarios.all: "all" is reserved: "all" and "auto" are suite keywords, and names starting with "auto-" belong to generated scenarios`,
		}},
		{"suite entries", "scenarios:\n  db-blip: {}\nsuites:\n  pr: [db-blpi, auto]\n", []string{
			`13:8: suites.pr[0]: no scenario named "db-blpi" (did you mean "db-blip"?)`,
			`13:17: suites.pr[1]: auto is a generated scenario; set auto_scenarios: true to generate them`,
		}},
		{"auto scenario names", "auto_scenarios: true\nsuites:\n  pr: [auto-crash-payments, auto-down-checkout-payments, auto-down-traffic-checkout, auto]\n", []string{
			`12:58: suites.pr[2]: no scenario named "auto-down-traffic-checkout"`,
		}},
		{"checks", "checks:\n  - sql: { on: pg, query: x, expect: empty }\n  - slo: { service: postgres, measure: p99, max: 1% }\n  - metric: { query: up }\n  - { recovers: { service: payments, within: 1s }, volumes: [x] }\n  - { name: dup, metric: { query: up, max: 1 } }\n  - { name: dup, recovers: { service: checkout, within: 1s }, sql: { on: postgres, query: x, expect: 0 } }\n", []string{
			`11:16: checks[0].sql.on: no connection named "pg"; declare it under connections (declared: postgres)`,
			`12:21: checks[1].slo.service: postgres's declared inbound edges are all tcp, where the proxy sees bytes, not requests; this check needs an http or grpc edge into postgres`,
			`12:50: checks[1].slo.max: "1%" is not a duration; p99 takes one such as 250ms`,
			`13:13: checks[2].metric: a metric check needs a bound: max, min, or within (relative to base)`,
			`14:52: checks[3].volumes: volumes belongs to a code check (an image: entry), not a recovers check`,
			`16:5: checks[5]: a check has exactly one kind; this one has sql and recovers`,
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

// Edge problems need their own file: base already declares edges.
func TestEdgeProblems(t *testing.T) {
	_, err := config.Parse("kazu.yaml", []byte(`system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  checkout -> checkout: tcp
  checkout -> traffic: http
  payments -> fraud: { protocol: tcp, tls: { ca_cert: a, ca_key: b } }
  checkout->payments: http
  checkout -> payments: grpc
`))
	want := []string{
		`kazu.yaml:4:3: edges["checkout -> checkout"]: an edge needs two different services; checkout calls itself`,
		`kazu.yaml:5:3: edges["checkout -> traffic"]: traffic only sends requests, so nothing calls it; edges into traffic aren't allowed`,
		`kazu.yaml:6:44: edges["payments -> fraud"].tls: tls applies to http and grpc edges, where the proxy terminates TLS for request-level faults; network faults on a tcp edge need no certificates`,
		`kazu.yaml:8:3: edges["checkout -> payments"]: checkout -> payments is declared twice`,
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("got:\n%v\nwant:\n%s", err, strings.Join(want, "\n"))
	}
}

func TestLoadChecksSystemPath(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := write("kazu.yaml", "system: compose.yml\ntraffic: { image: grafana/k6:1.0.0 }\n")
	_, err := config.Load(p)
	if want := p + ":1:9: system: compose.yml not found (paths are relative to the config file)"; err == nil || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}

	write("compose.yml", "services: {}\n")
	if _, err := config.Load(p); err != nil {
		t.Errorf("with the file present: %v", err)
	}

	p = write("kazu.yaml", "system: { manifests: compose.yml }\ntraffic: { image: grafana/k6:1.0.0 }\n")
	_, err = config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "system.manifests: compose.yml is not a directory") {
		t.Errorf("manifests naming a file: %v", err)
	}
}
