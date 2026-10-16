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

// A request-count trigger on a TCP edge is a config error before any trial.
func TestRequestCountTriggerOnTCPEdge(t *testing.T) {
	_, err := config.ParseContract("kazu.yaml", []byte(base+`
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

// Each rule the validator owns has a failing case with its exact message.
// Rules the schema owns (fields per kind, one target, one check kind, ...)
// are in errors_test.go.
func TestRules(t *testing.T) {
	tests := []struct {
		name string
		yaml string // appended to base
		want []string
	}{
		{"undeclared edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payment, fault: latency, delay: 1s }\n", []string{
			`13:17: scenarios.a.faults[0].edge: checkout -> payment is not a declared edge; faults go only on edges listed under edges (did you mean "checkout -> payments"?)`,
		}},
		{"undeclared edge still gets its values checked", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payment, fault: latency, delay: 0s, for: 0s }\n", []string{
			`13:17: scenarios.a.faults[0].edge: checkout -> payment is not a declared edge; faults go only on edges listed under edges (did you mean "checkout -> payments"?)`,
			`13:61: scenarios.a.faults[0].delay: must be longer than zero`,
			`13:70: scenarios.a.faults[0].for: must be longer than zero`,
		}},
		{"request fault on a tcp edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> postgres, fault: timeout, rate: 5% }\n", []string{
			`13:17: scenarios.a.faults[0].edge: a timeout fault needs an http or grpc edge; checkout -> postgres is tcp, so the proxy sees bytes, not requests`,
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
		{"for on a crash", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: crash, for: 5s }\n", []string{
			`13:49: scenarios.a.faults[0].for: a crash is instant; restart_after sets how long the service stays down`,
		}},
		{"zero restart_after", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: crash, restart_after: 0s }\n", []string{
			`13:59: scenarios.a.faults[0].restart_after: must be longer than zero`,
		}},
		{"jitter above delay", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency, delay: 100ms, jitter: 5s }\n", []string{
			`13:77: scenarios.a.faults[0].jitter: jitter (5s) is more than delay (100ms), so the delay could go negative`,
		}},
		{"zero rate", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: reset, rate: 0% }\n", []string{
			`13:59: scenarios.a.faults[0].rate: 0% would never fire; use a share above 0%`,
		}},
		{"zero load", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: cpu, load: 0% }\n", []string{
			`13:48: scenarios.a.faults[0].load: 0% would never fire; use a share above 0%`,
		}},
		{"zero bandwidth", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: bandwidth, limit: 0bps }\n", []string{
			`13:64: scenarios.a.faults[0].limit: must be above zero; use down to stop traffic entirely`,
		}},
		{"zero durations", "scenarios:\n  a:\n    duration: 0s\n    faults:\n      - { edge: checkout -> payments, fault: latency, delay: 0s, for: 0s }\n", []string{
			`12:15: scenarios.a.duration: must be longer than zero`,
			`14:62: scenarios.a.faults[0].delay: must be longer than zero`,
			`14:71: scenarios.a.faults[0].for: must be longer than zero`,
		}},
		{"duration shorter than its faults", "scenarios:\n  a:\n    duration: 10s\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20s, for: 5s }\n      - { edge: checkout -> payments, fault: down, after: 5s }\n", []string{
			`12:15: scenarios.a.duration: traffic stops at 10s, before faults[0] ends at 25s; the fault would not be exercised`,
		}},
		{"gRPC code on an HTTP edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: errors, rate: 5%, code: UNAVAILABLE }\n", []string{
			`13:70: scenarios.a.faults[0].code: "UNAVAILABLE" is not an HTTP error status; errors injects failures, so use a number from 400 to 599, such as 503`,
		}},
		{"quoted success code on an HTTP edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: errors, rate: 5%, code: '200' }\n", []string{
			`13:70: scenarios.a.faults[0].code: "200" is not an HTTP error status; errors injects failures, so use a number from 400 to 599, such as 503`,
		}},
		{"reserved scenario names", "scenarios:\n  auto-x: {}\n  all: {}\n", []string{
			`11:3: scenarios.auto-x: "auto-x" is reserved: "all" and "auto" are suite keywords, and names starting with "auto-" belong to generated scenarios`,
			`12:3: scenarios.all: "all" is reserved: "all" and "auto" are suite keywords, and names starting with "auto-" belong to generated scenarios`,
		}},
		{"suite entries", "scenarios:\n  db-blip: {}\nsuites:\n  pr: [db-blpi, auto, all]\n", []string{
			`13:8: suites.pr[0]: no scenario named "db-blpi" (did you mean "db-blip"?)`,
			`13:23: suites.pr[2]: "all" is written as the suite's value (pr: all), not as an entry in its list`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.ParseContract("kazu.yaml", []byte(base+tt.yaml))
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

// from names a fault in the same scenario, not itself, with an end if its
// end is used, and faults don't count from each other in a loop.
func TestAnchorRules(t *testing.T) {
	tests := []struct {
		name, yaml string
		want       []string
	}{
		{"unknown fault", "scenarios:\n  a:\n    faults:\n      - { name: slow-pay, edge: checkout -> payments, fault: down, for: 5s }\n      - { edge: checkout -> postgres, fault: down, from: slow-pya }\n", []string{
			`14:58: scenarios.a.faults[1].from: no fault named "slow-pya" in this scenario (did you mean "slow-pay"?)`,
		}},
		{"itself", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down, for: 5s, from: x }\n", []string{
			`13:76: scenarios.a.faults[0].from: a fault can't count from itself`,
		}},
		{"end of a fault without for", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down }\n      - { edge: checkout -> postgres, fault: down, from: [{ start: x }, x] }\n", []string{
			`14:73: scenarios.a.faults[1].from[1]: x has no for, so it lasts until traffic ends; give it a for, or count from { start: x }`,
		}},
		{"end of a crash", "scenarios:\n  a:\n    faults:\n      - { name: x, service: payments, fault: crash }\n      - { edge: checkout -> postgres, fault: down, from: x }\n", []string{
			`14:58: scenarios.a.faults[1].from: a crash is instant, so x has no end; count from { start: x }`,
		}},
		{"two faults with one name", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down }\n      - { name: x, edge: checkout -> postgres, fault: down }\n", []string{
			`14:17: scenarios.a.faults[1].name: another fault in this scenario is already named "x"`,
		}},
		{"a loop", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down, for: 5s, from: z }\n      - { name: y, edge: checkout -> postgres, fault: down, for: 5s, from: { start: x } }\n      - { name: z, edge: payments -> postgres, fault: down, for: 5s, from: y }\n", []string{
			`13:76: scenarios.a.faults[0].from: faults count from each other in a loop: x -> z -> y -> x`,
		}},
		{"names belong to their scenario", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down, for: 5s }\n  b:\n    faults:\n      - { edge: checkout -> payments, fault: down, from: x }\n", []string{
			`16:58: scenarios.b.faults[0].from: no fault named "x" in this scenario`,
		}},
		{"scenario check named like a top-level one", "checks:\n  - { name: c, sql: { on: postgres, query: x, expect: empty } }\nscenarios:\n  a:\n    checks:\n      - { name: c, sql: { on: postgres, query: y, expect: empty } }\n", []string{
			`15:17: scenarios.a.checks[0].name: another check is already named "c"`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.ParseContract("kazu.yaml", []byte(base+tt.yaml))
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

// Check rules need a second connection, so they get their own document.
func TestCheckRules(t *testing.T) {
	_, err := config.ParseContract("kazu.yaml", []byte(`system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  traffic -> checkout: http
  checkout -> payments: http
  checkout -> postgres: tcp
connections:
  postgres: postgres://postgres:dev@postgres/shop
  api: http://checkout:8080
checks:
  - sql: { on: pg, query: x, expect: empty }
  - sql: { on: api, query: x, expect: empty }
  - http: { on: postgres, path: /x, expect: { status: 200 } }
  - slo: { service: postgres, measure: p99, max: 0s }
  - metric: { query: up, min: 2, max: 1 }
  - { name: dup, metric: { query: up, max: 1 } }
  - { name: dup, recovers: { service: paymnets, within: 1s } }
`))
	want := []string{
		`kazu.yaml:11:16: checks[0].sql.on: no connection named "pg"; declare it under connections (declared: postgres, api)`,
		`kazu.yaml:12:16: checks[1].sql.on: api is a http:// connection; this check needs a postgres:// one`,
		`kazu.yaml:13:17: checks[2].http.on: postgres is a postgres:// connection; this check needs a http:// one`,
		`kazu.yaml:14:21: checks[3].slo.service: postgres's declared inbound edges are all tcp, where the proxy sees bytes, not requests; this check needs an http or grpc edge into postgres`,
		`kazu.yaml:14:50: checks[3].slo.max: must be longer than zero`,
		`kazu.yaml:15:13: checks[4].metric: min (2) is above max (1), so no value can pass`,
		`kazu.yaml:17:13: checks[6].name: another check is already named "dup"`,
		`kazu.yaml:17:39: checks[6].recovers.service: paymnets has no declared inbound edges, so no proxy measures it; declare an http or grpc edge into paymnets (did you mean "payments"?)`,
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("got:\n%v\nwant:\n%s", err, strings.Join(want, "\n"))
	}
}

// mixed adds a grpc edge and a service with both tcp and http inbound edges.
const mixed = `system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  traffic -> checkout: http
  checkout -> payments: http
  worker -> payments: tcp
  payments -> fraud: grpc
connections:
  postgres: postgres://postgres:dev@postgres/shop
`

// Shapes that must pass: the positive side of the rules above.
func TestRulesAccept(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"edge spacing is normalised", "scenarios:\n  a:\n    faults:\n      - { edge: checkout->payments, fault: latency, delay: 1s }\n"},
		{"request count on an http edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: errors, rate: 5%, code: 503, after_requests: 10 }\n"},
		{"request count on a service with mixed inbound edges", "scenarios:\n  a:\n    faults:\n      - { service: payments, fault: crash, after_requests: 3, restart_after: 1s }\n"},
		{"measured service with mixed inbound edges", "checks:\n  - recovers: { service: payments, within: 30s }\n  - slo: { service: payments, measure: p99, max: 250ms }\n  - no_regression: { service: payments, measure: error_rate, within: 10% }\n"},
		{"tls on grpc", "scenarios: {}\n"},
		{"jitter equal to delay", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency, delay: 100ms, jitter: 100ms }\n"},
		{"anchors: a join, and the start of a fault without for", "scenarios:\n  a:\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down }\n      - { name: y, edge: worker -> payments, fault: down, for: 5s, from: { start: x } }\n      - { edge: payments -> fraud, fault: down, from: [y, { start: x }], after: 1s }\n"},
		{"duration check skips anchored faults", "scenarios:\n  a:\n    duration: 10s\n    faults:\n      - { name: x, edge: checkout -> payments, fault: down, after: 1s, for: 5s }\n      - { edge: worker -> payments, fault: down, from: x, after: 20s }\n"},
		{"duration that covers its faults", "scenarios:\n  a:\n    duration: 30s\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20s, for: 10s }\n"},
		{"zero tolerance", "checks:\n  - no_regression: { service: payments, measure: p99, within: 0% }\n"},
		{"generated scenarios, which the build check rejects", "suites: { pr: [auto, auto-crash-payments, auto-down-checkout-payments] }\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.ParseContract("kazu.yaml", []byte(mixed+tt.yaml)); err != nil {
				t.Errorf("rejected:\n%v", err)
			}
		})
	}
}

// Codes are canonical after loading: digits on http edges, upper-case names
// on grpc edges, whatever the file wrote.
func TestStatusCodes(t *testing.T) {
	accept := map[string]string{
		"payments -> fraud, fault: errors, rate: 5%, code: 14":           "UNAVAILABLE",
		"payments -> fraud, fault: errors, rate: 5%, code: unavailable":  "UNAVAILABLE",
		"payments -> fraud, fault: errors, rate: 5%, code: DATA_LOSS":    "DATA_LOSS",
		"checkout -> payments, fault: errors, rate: 5%, code: 503":       "503",
		"checkout -> payments, fault: errors, rate: 5%, code: '503'":     "503",
		"payments -> fraud, fault: timeout, rate: 5%, code: UNAVAILABLE": "UNAVAILABLE",
	}
	for fields, want := range accept {
		src := mixed + "scenarios:\n  a:\n    faults:\n      - { edge: " + fields + " }\n"
		cfg, err := config.ParseContract("kazu.yaml", []byte(src))
		if err != nil {
			if strings.Contains(fields, "timeout") {
				continue // timeout takes no code; the schema says so
			}
			t.Errorf("%s: %v", fields, err)
			continue
		}
		if got := cfg.Scenarios[0].Faults[0].Code; got != want {
			t.Errorf("%s: code = %q, want %q", fields, got, want)
		}
	}
	reject := map[string]string{
		"payments -> fraud, code: 17":          `17 is not a gRPC status code (0 to 16)`,
		"payments -> fraud, code: UNAVAILABEL": `"UNAVAILABEL" is not a gRPC status code, such as UNAVAILABLE (did you mean "UNAVAILABLE"?)`,
		"payments -> fraud, code: 503":         `503 is not a gRPC status code (0 to 16)`,
		"payments -> fraud, code: OK":          `OK is success; errors injects failures, so use a code such as UNAVAILABLE`,
		"payments -> fraud, code: 0":           `OK is success; errors injects failures, so use a code such as UNAVAILABLE`,
		"checkout -> payments, code: 200":      `"200" is not an HTTP error status; errors injects failures, so use a number from 400 to 599, such as 503`,
		"checkout -> payments, code: 600":      `"600" is not an HTTP error status; errors injects failures, so use a number from 400 to 599, such as 503`,
	}
	for field, want := range reject {
		edge, code, _ := strings.Cut(field, ", ")
		src := mixed + "scenarios:\n  a:\n    faults:\n      - { edge: " + edge + ", fault: errors, rate: 5%, " + code + " }\n"
		_, err := config.ParseContract("kazu.yaml", []byte(src))
		if err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("%s: got %v\nwant suffix %s", field, err, want)
		}
	}
}

// Edge problems need their own file: base already declares edges.
func TestEdgeProblems(t *testing.T) {
	_, err := config.ParseContract("kazu.yaml", []byte(`system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  checkout -> checkout: tcp
  checkout -> traffic: http
  payments -> fraud: { protocol: tcp, tls: { ca_cert: a, ca_key: b } }
  checkout->payments: http
  checkout -> payments: grpc
  payments -> auth: { protocol: grpc, tls: { ca_cert: a, ca_key: b } }
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
	abs := write("kazu-abs.yaml", "system: "+filepath.Join(dir, "compose.yml")+"\ntraffic: { image: grafana/k6:1.0.0 }\n")
	if _, err := config.Load(abs); err != nil {
		t.Errorf("with an absolute path: %v", err)
	}

	p = write("kazu.yaml", "system: { manifests: compose.yml }\ntraffic: { image: grafana/k6:1.0.0 }\n")
	_, err = config.LoadContract(p)
	if err == nil || !strings.Contains(err.Error(), "system.manifests: compose.yml is not a directory") {
		t.Errorf("manifests naming a file: %v", err)
	}
	p = write("kazu.yaml", "system: "+dir+"\ntraffic: { image: grafana/k6:1.0.0 }\n")
	_, err = config.Load(p)
	if err == nil || !strings.Contains(err.Error(), "is a directory; for rendered manifests write system: { manifests:") {
		t.Errorf("compose naming a directory: %v", err)
	}
}
