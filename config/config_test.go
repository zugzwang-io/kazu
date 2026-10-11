package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/config"
)

// The two configs in DESIGN.md load cleanly and decode to what they say.
func TestReference(t *testing.T) {
	cfg, err := config.Load("testdata/reference.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.System.Compose != "docker-compose.yml" {
		t.Errorf("system = %+v", cfg.System)
	}
	wantTraffic := config.Service{
		Image:       "grafana/k6:1.0.0",
		Command:     []string{"run", "/load/traffic.js"},
		Volumes:     []string{"./load:/load:ro"},
		Environment: map[string]string{"FLOW": "checkout"},
	}
	if !reflect.DeepEqual(cfg.Traffic, wantTraffic) {
		t.Errorf("traffic = %+v", cfg.Traffic)
	}

	var edges []string
	for _, e := range cfg.Edges {
		edges = append(edges, e.Name()+" "+string(e.Protocol))
	}
	wantEdges := []string{
		"traffic -> checkout http", "checkout -> payments http", "checkout -> postgres tcp",
		"payments -> fraud grpc", "checkout -> auth http",
	}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges in file order = %q", edges)
	}
	if auth, _ := cfg.Edge("checkout -> auth"); auth.TLS == nil || auth.TLS.CAKey != "${env:KAZU_TEST_CA_KEY}" {
		t.Errorf("tls = %+v", auth.TLS)
	}
	if !cfg.Edges[0].UserFacing() || cfg.Edges[1].UserFacing() {
		t.Error("only edges from traffic are user-facing")
	}

	var names []string
	for _, s := range cfg.Scenarios {
		names = append(names, s.Name)
	}
	wantNames := []string{"slow-payments", "db-blip", "payments-dies-midflight", "double-click", "browse-under-outage", "soak", "rare-errors"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("scenarios in file order = %q", names)
	}

	slow, _ := cfg.Scenario("slow-payments")
	wantSlow := config.Fault{Edge: "checkout -> payments", Kind: config.Latency, Delay: 300 * time.Millisecond, Jitter: 50 * time.Millisecond, After: 20 * time.Second}
	if !reflect.DeepEqual(slow.Faults, []config.Fault{wantSlow}) {
		t.Errorf("slow-payments faults = %+v", slow.Faults)
	}
	midflight, _ := cfg.Scenario("payments-dies-midflight")
	if f := midflight.Faults[0]; f.Service != "payments" || f.Kind != config.Crash || f.AfterRequests != 200 || f.RestartAfter != 5*time.Second {
		t.Errorf("payments-dies-midflight fault = %+v", f)
	}
	browse, _ := cfg.Scenario("browse-under-outage")
	wantEnv := []config.ServiceEnv{{Service: "traffic", Vars: map[string]string{"FLOW": "browse", "RATE": "50"}}}
	if !reflect.DeepEqual(browse.Env, wantEnv) {
		t.Errorf("browse-under-outage env = %+v", browse.Env)
	}
	soak, _ := cfg.Scenario("soak")
	if soak.Duration != 180*time.Second || soak.Traffic == nil || !reflect.DeepEqual(soak.Traffic.Command, []string{"run", "/load/soak.js"}) || soak.Traffic.Volumes != nil {
		t.Errorf("soak = %+v, traffic %+v", soak, soak.Traffic)
	}
	rare, _ := cfg.Scenario("rare-errors")
	if f := rare.Faults[0]; rare.NotExercised != config.Warn || f.Rate != 1 || f.Code != "UNAVAILABLE" {
		t.Errorf("rare-errors = %+v", rare)
	}

	if d := cfg.Defaults; d.LatencyWithin != 20 || d.ErrorRateWithin != 0.5 || d.RecoversWithin != time.Minute || !reflect.DeepEqual(d.Languages, []string{"go"}) {
		t.Errorf("defaults = %+v", d)
	}

	var checks []string
	for _, c := range cfg.Checks {
		checks = append(checks, c.Name)
	}
	wantChecks := []string{"image-1", "sql-2", "recovers-3", "no_regression-4", "slo-5", "metric-6"}
	if !reflect.DeepEqual(checks, wantChecks) {
		t.Errorf("check names = %q", checks)
	}
	if sql := cfg.Checks[1].SQL; !sql.ExpectEmpty || sql.On != "postgres" {
		t.Errorf("sql = %+v", sql)
	}
	if slo := cfg.Checks[4].SLO; slo.Measure != config.ErrorRate || slo.MaxRate != 1 {
		t.Errorf("slo = %+v", slo)
	}
	if m := cfg.Checks[5].Metric; m.Within == nil || *m.Within != 50 || m.Max != nil {
		t.Errorf("metric = %+v", m)
	}

	wantSuites := []config.Suite{
		{Name: "pr", Scenarios: []string{"slow-payments", "db-blip", "double-click"}},
		{Name: "release", All: true},
	}
	if !reflect.DeepEqual(cfg.Suites, wantSuites) {
		t.Errorf("suites = %+v", cfg.Suites)
	}
	wantGate := config.Gate{FailOn: config.FailOnRegression, Flaky: config.Warn, NotExercised: config.Fail, NoData: config.Fail, Underpowered: config.Warn}
	if cfg.Gate != wantGate {
		t.Errorf("gate = %+v", cfg.Gate)
	}
}

// The example in the README is a config people will copy.
func TestReadmeExample(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(readme), "```yaml\n")
	example, _, ok2 := strings.Cut(rest, "```")
	if !ok || !ok2 {
		t.Fatal("README.md has no yaml example")
	}
	if _, err := config.Parse("README.md", []byte(example)); err != nil {
		t.Error(err)
	}
}

func TestWalkthrough(t *testing.T) {
	cfg, err := config.Load("testdata/walkthrough.yaml")
	if err != nil {
		t.Fatal(err)
	}
	stripe, _ := cfg.Scenario("stripe-errors")
	if f := stripe.Faults[0]; f.Kind != config.Errors || f.Code != "503" || f.Rate != 10 {
		t.Errorf("stripe-errors fault = %+v", f)
	}
	if m := cfg.Checks[1].Metric; m.Max == nil || *m.Max != 3 {
		t.Errorf("metric = %+v", m)
	}
}

// A minimal config gets the documented defaults.
func TestDefaults(t *testing.T) {
	cfg, err := config.Parse("kazu.yaml", []byte("system: docker-compose.yml\ntraffic: { image: grafana/k6:1.0.0 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Defaults{LatencyWithin: 20, ErrorRateWithin: 0.5, RecoversWithin: time.Minute}
	if !reflect.DeepEqual(cfg.Defaults, want) {
		t.Errorf("defaults = %+v", cfg.Defaults)
	}
	if cfg.Gate.Trials != 0 || cfg.Gate.NotExercised != config.Fail || cfg.Gate.NoData != config.Fail || cfg.Gate.Underpowered != config.Warn {
		t.Errorf("gate = %+v", cfg.Gate)
	}
}

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

// base declares a small system; cases append to it, so line 10 is the
// first line of a case.
const base = `system: docker-compose.yml
traffic: { image: grafana/k6:1.0.0 }
edges:
  traffic -> checkout: http
  checkout -> payments: http
  checkout -> postgres: tcp
  payments -> postgres: tcp
connections:
  postgres: postgres://postgres:dev@postgres/shop
`

func TestProblems(t *testing.T) {
	tests := []struct {
		name string
		yaml string // appended to base
		want []string
	}{
		{"unknown top-level key", "scenarioz: {}\n", []string{
			`10:1: scenarioz: unknown key "scenarioz" (did you mean "scenarios"?)`,
		}},
		{"unknown fault key", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency, delay: 1s, dealy: 2s }\n", []string{
			`13:66: scenarios.a.faults[0].dealy: unknown key "dealy" (did you mean "delay"?)`,
		}},
		{"undeclared edge", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payment, fault: latency, delay: 1s }\n", []string{
			`13:17: scenarios.a.faults[0].edge: checkout -> payment is not a declared edge; faults go only on edges listed under edges (did you mean "checkout -> payments"?)`,
		}},
		{"edge spacing is normalised", "scenarios:\n  a:\n    faults:\n      - { edge: checkout->payments, fault: latency, delay: 1s }\n", nil},
		{"unknown fault kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latncy, delay: 1s }\n", []string{
			`13:46: scenarios.a.faults[0].fault: "latncy" is not one of "latency", "down", "blackhole", "reset", "bandwidth", "errors", "timeout", "crash", "pause", "cpu", "memory", "disk_full" (did you mean "latency"?)`,
		}},
		{"missing required field", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency }\n", []string{
			`13:9: scenarios.a.faults[0]: missing key "delay"`,
		}},
		{"duration without a unit", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20 }\n", []string{
			`13:59: scenarios.a.faults[0].after: expected a duration such as 300ms, 20s or 1m30s, got a number`,
		}},
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
		{"empty suite", "suites:\n  pr: []\n", []string{
			`11:7: suites.pr: must not be empty`,
		}},
		{"checks", "checks:\n  - sql: { on: pg, query: x, expect: empty }\n  - slo: { service: postgres, measure: p99, max: 1% }\n  - metric: { query: up }\n  - { recovers: { service: payments, within: 1s }, volumes: [x] }\n  - { name: dup, metric: { query: up, max: 1 } }\n  - { name: dup, recovers: { service: checkout, within: 1s }, sql: { on: postgres, query: x, expect: 0 } }\n", []string{
			`11:16: checks[0].sql.on: no connection named "pg"; declare it under connections (declared: postgres)`,
			`12:21: checks[1].slo.service: postgres's declared inbound edges are all tcp, where the proxy sees bytes, not requests; this check needs an http or grpc edge into postgres`,
			`12:50: checks[1].slo.max: "1%" is not a duration; p99 takes one such as 250ms`,
			`13:13: checks[2].metric: a metric check needs a bound: max, min, or within (relative to base)`,
			`14:52: checks[3].volumes: volumes belongs to a code check (an image: entry), not a recovers check`,
			`16:5: checks[5]: a check has exactly one kind; this one has sql and recovers`,
		}},
		{"gate", "gate: { trials: sometimes, flaky: maybe }\n", []string{
			`10:17: gate.trials: "sometimes" is not allowed here; the only text value is "auto"`,
			`10:35: gate.flaky: "maybe" is not one of "fail", "warn"`,
		}},
		{"invalid YAML", "x: [\n", []string{
			`10:1: invalid YAML: did not find expected node content`,
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

// YAML anchors and merge keys mean what they mean in compose files.
func TestAnchorsAndMerges(t *testing.T) {
	cfg, err := config.Parse("kazu.yaml", []byte(base+`
x-fault: &slow { fault: latency, delay: 1s, after: 20s }
scenarios:
  a:
    faults:
      - { <<: *slow, edge: checkout -> payments }
      - { <<: *slow, edge: payments -> postgres, delay: 2s }
`))
	if err != nil {
		// x- keys are compose's extension convention; Kazu doesn't take them.
		if !strings.Contains(err.Error(), `unknown key "x-fault"`) {
			t.Fatal(err)
		}
	}
	if cfg != nil {
		t.Fatal("x-fault should be rejected")
	}

	cfg, err = config.Parse("kazu.yaml", []byte(base+`
scenarios:
  a:
    faults:
      - &slow { edge: checkout -> payments, fault: latency, delay: 1s, after: 20s }
      - { <<: *slow, edge: payments -> postgres, delay: 2s }
`))
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Scenarios[0].Faults[1]
	if f.Edge != "payments -> postgres" || f.Delay != 2*time.Second || f.After != 20*time.Second {
		t.Errorf("merged fault = %+v", f)
	}
}

func TestEnvironmentForms(t *testing.T) {
	cfg, err := config.Parse("kazu.yaml", []byte(`system: docker-compose.yml
traffic:
  image: grafana/k6:1.0.0
  environment: [FLOW=browse, RATE=50, "EMPTY="]
scenarios:
  a:
    env: { checkout: { RETRIES: 3, FAST: true } }
    traffic: { environment: { RATE: 10 } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"FLOW": "browse", "RATE": "50", "EMPTY": ""}; !reflect.DeepEqual(cfg.Traffic.Environment, want) {
		t.Errorf("list environment = %v", cfg.Traffic.Environment)
	}
	s := cfg.Scenarios[0]
	if want := map[string]string{"RETRIES": "3", "FAST": "true"}; !reflect.DeepEqual(s.Env[0].Vars, want) {
		t.Errorf("scenario env = %v", s.Env[0].Vars)
	}
	if s.Traffic.Command != nil || s.Traffic.Environment["RATE"] != "10" {
		t.Errorf("traffic override = %+v", s.Traffic)
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

func TestEmptyFile(t *testing.T) {
	_, err := config.Parse("kazu.yaml", nil)
	if want := "kazu.yaml: the file is empty; it needs at least system and traffic"; err == nil || err.Error() != want {
		t.Errorf("got %v", err)
	}
	_, err = config.Parse("kazu.yaml", []byte("system: x\n"))
	if want := `kazu.yaml:1:1: missing key "traffic"`; err == nil || err.Error() != want {
		t.Errorf("got %v", err)
	}
}
