package config_test

import (
	"os"
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
	wantConns := []config.Connection{{Name: "postgres", URL: "postgres://postgres:dev@postgres/checkout"}}
	if !reflect.DeepEqual(cfg.Connections, wantConns) {
		t.Errorf("connections = %+v", cfg.Connections)
	}
	if cfg.Telemetry.Collector != "otel-collector" || !cfg.AutoScenarios {
		t.Errorf("telemetry = %+v, auto_scenarios = %v", cfg.Telemetry, cfg.AutoScenarios)
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
	blip, _ := cfg.Scenario("db-blip")
	wantBlip := config.Fault{Service: "postgres", Kind: config.Down, After: 20 * time.Second, For: 15 * time.Second}
	if !reflect.DeepEqual(blip.Faults, []config.Fault{wantBlip}) {
		t.Errorf("db-blip faults = %+v", blip.Faults)
	}
	midflight, _ := cfg.Scenario("payments-dies-midflight")
	if f := midflight.Faults[0]; f.Service != "payments" || f.Kind != config.Crash || f.AfterRequests != 200 || f.RestartAfter != 5*time.Second {
		t.Errorf("payments-dies-midflight fault = %+v", f)
	}
	double, _ := cfg.Scenario("double-click")
	if len(double.Faults) != 0 || !reflect.DeepEqual(double.Env, []config.ServiceEnv{{Service: "traffic", Vars: map[string]string{"DOUBLE_SUBMIT": "1"}}}) {
		t.Errorf("double-click = %+v", double)
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

	wantDefaults := config.Defaults{LatencyWithin: 20, ErrorRateWithin: 0.5, RecoversWithin: time.Minute, Languages: []config.Language{config.Go}, Off: nil}
	if !reflect.DeepEqual(cfg.Defaults, wantDefaults) {
		t.Errorf("defaults = %+v", cfg.Defaults)
	}

	var checks []string
	for _, c := range cfg.Checks {
		checks = append(checks, c.Name)
	}
	wantChecks := []string{"image-1", "sql-2", "recovers-3", "no_regression-4", "slo-5", "metric-6"}
	if !reflect.DeepEqual(checks, wantChecks) {
		t.Errorf("check names = %q", checks)
	}
	wantImage := &config.Service{Image: "ghcr.io/zugzwang-io/kazu-python:1", Volumes: []string{"./checks:/checks:ro"}}
	if !reflect.DeepEqual(cfg.Checks[0].Image, wantImage) {
		t.Errorf("image check = %+v", cfg.Checks[0].Image)
	}
	if sql := cfg.Checks[1].SQL; !sql.ExpectEmpty || sql.On != "postgres" || !strings.HasPrefix(sql.Query, "select order_id") {
		t.Errorf("sql = %+v", sql)
	}
	if r := cfg.Checks[2].Recovers; *r != (config.Recovers{Service: "payments", Within: 30 * time.Second}) {
		t.Errorf("recovers = %+v", r)
	}
	if nr := cfg.Checks[3].NoRegression; *nr != (config.NoRegression{Service: "payments", Measure: config.P99, Within: 10}) {
		t.Errorf("no_regression = %+v", nr)
	}
	if slo := cfg.Checks[4].SLO; slo.Measure != config.ErrorRate || slo.MaxRate != 1 || slo.MaxLatency != 0 {
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

// Every yaml example in the README is a config people will copy.
func TestReadmeExamples(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	examples := yamlBlocks(string(readme))
	if len(examples) == 0 {
		t.Fatal("README.md has no yaml example")
	}
	for i, example := range examples {
		if _, err := config.Parse("README.md", []byte(example)); err != nil {
			t.Errorf("yaml block %d: %v", i, err)
		}
	}
}

func TestWalkthrough(t *testing.T) {
	cfg, err := config.Load("testdata/walkthrough.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.Collector != "otel-collector" {
		t.Errorf("flow-style telemetry = %+v", cfg.Telemetry)
	}
	stripe, _ := cfg.Scenario("stripe-errors")
	if f := stripe.Faults[0]; f.Kind != config.Errors || f.Code != "503" || f.Rate != 10 {
		t.Errorf("stripe-errors fault = %+v", f)
	}
	if img := cfg.Checks[0].Image; img == nil || img.Image != "ghcr.io/zugzwang-io/kazu-python:1" {
		t.Errorf("image check = %+v", img)
	}
	if m := cfg.Checks[1].Metric; m.Max == nil || *m.Max != 3 || m.Within != nil {
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

func parse(t *testing.T, tail string) *config.Config {
	t.Helper()
	cfg, err := config.Parse("kazu.yaml", []byte(base+tail))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func firstFault(t *testing.T, fields string) config.Fault {
	t.Helper()
	cfg := parse(t, "scenarios: { a: { faults: [ { "+fields+" } ] } }\n")
	return cfg.Scenarios[0].Faults[0]
}

// Every string format in ADR-0002 decodes to the value it names.
func TestFormats(t *testing.T) {
	durations := map[string]time.Duration{"300ms": 300 * time.Millisecond, "1.5s": 1500 * time.Millisecond, "1m30s": 90 * time.Second, "2h": 2 * time.Hour, "0s": 0}
	for text, want := range durations {
		if got := firstFault(t, "edge: checkout -> payments, fault: latency, delay: "+text).Delay; got != want {
			t.Errorf("delay %s = %v, want %v", text, got, want)
		}
	}
	rates := map[string]config.Percent{"2%": 2, "0.5%": 0.5, "100%": 100}
	for text, want := range rates {
		if got := firstFault(t, "edge: checkout -> payments, fault: reset, rate: "+text).Rate; got != want {
			t.Errorf("rate %s = %v, want %v", text, got, want)
		}
	}
	limits := map[string]config.BitRate{"100bps": 100, "64kbps": 64_000, "1.5mbps": 1_500_000, "2gbps": 2_000_000_000}
	for text, want := range limits {
		if got := firstFault(t, "edge: checkout -> payments, fault: bandwidth, limit: "+text).Limit; got != want {
			t.Errorf("limit %s = %v, want %v", text, got, want)
		}
	}
	if f := firstFault(t, "service: payments, fault: cpu, load: 90%"); f.Load != 90 {
		t.Errorf("load = %v", f.Load)
	}
	if f := firstFault(t, "service: payments, fault: crash, after_requests: 0x10"); f.AfterRequests != 16 {
		t.Errorf("after_requests 0x10 = %d, want 16 (YAML's value, not the text)", f.AfterRequests)
	}
	cfg := parse(t, "defaults: { latency_within: 150%, error_rate_within: 0.25pp, recovers_within: 1m30s }\ngate: { trials: 5 }\n")
	if d := cfg.Defaults; d.LatencyWithin != 150 || d.ErrorRateWithin != 0.25 || d.RecoversWithin != 90*time.Second || cfg.Gate.Trials != 5 {
		t.Errorf("defaults = %+v, trials = %d", d, cfg.Gate.Trials)
	}
}

// Numbers the schema accepts but Go can't hold, or that YAML writes in a form
// the schema can't tell from an integer, are errors rather than zeros.
func TestRejectsLossyNumbers(t *testing.T) {
	tests := []struct{ name, tail, want string }{
		{"float as a request count", "scenarios: { a: { faults: [ { service: payments, fault: crash, after_requests: 200.0 } ] } }\n", "expected a whole number, got 200.0"},
		{"exponent as a request count", "scenarios: { a: { faults: [ { service: payments, fault: crash, after_requests: 2e2 } ] } }\n", "expected a whole number, got 2e2"},
		{"float trials", "gate: { trials: 5.0 }\n", "expected a whole number, got 5.0"},
		{"oversized trials", "gate: { trials: 99999999999999999999 }\n", "expected a whole number"},
		{"float status", "checks: [ { http: { on: postgres, path: /x, expect: { status: 200.0 } } } ]\n", "expected a whole number, got 200.0"},
		{"duration overflow", "scenarios: { a: { faults: [ { service: payments, fault: down, after: 9999999999999h } ] } }\n", "is not a duration Kazu can hold"},
		{"percent overflow", "defaults: { latency_within: 1" + strings.Repeat("0", 400) + "% }\n", "is too large a number"},
		{"bandwidth overflow", "scenarios: { a: { faults: [ { edge: checkout -> payments, fault: bandwidth, limit: 99999999999gbps } ] } }\n", "is too large a bandwidth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("kazu.yaml", []byte(base+tt.tail))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.HasPrefix(err.Error(), "kazu.yaml: ") {
				t.Errorf("error = %v, want %q with the file name", err, tt.want)
			}
		})
	}
}

// Edge names are canonical however the file spaces them, on both sides of
// the lookup.
func TestEdgeNameSpacing(t *testing.T) {
	for _, spelling := range []string{"a -> b", "a->b", "  a  ->   b  "} {
		cfg, err := config.Parse("kazu.yaml", []byte("system: x\ntraffic: { image: x }\nedges: { '"+spelling+"': http }\nscenarios: { s: { faults: [ { edge: '"+spelling+"', fault: down } ] } }\n"))
		if err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if e := cfg.Edges[0]; e.Caller != "a" || e.Callee != "b" || e.Name() != "a -> b" {
			t.Errorf("%q: edge = %+v", spelling, e)
		}
		if f := cfg.Scenarios[0].Faults[0]; f.Edge != "a -> b" {
			t.Errorf("%q: fault edge = %q", spelling, f.Edge)
		}
		if _, ok := cfg.Edge(cfg.Scenarios[0].Faults[0].Edge); !ok {
			t.Errorf("%q: fault's edge does not resolve", spelling)
		}
	}
}

func TestChecks(t *testing.T) {
	cfg := parse(t, `connections: { api: http://checkout:8080 }
checks:
  - http: { on: api, method: POST, path: /orders, expect: { status: 201, body: ord_ } }
  - http: { on: api, path: /healthz, expect: { status: 200 } }
  - { name: settled, sql: { on: postgres, query: select 1, expect: 1 } }
  - sql: { on: postgres, query: select 1, expect: "empty" }
  - slo: { service: checkout, measure: p99, max: 250ms }
  - metric: { query: up, min: 1, max: 3, no_data: warn }
`)
	want0 := &config.HTTPRequest{On: "api", Method: "POST", Path: "/orders", ExpectStatus: 201, ExpectBody: "ord_"}
	if !reflect.DeepEqual(cfg.Checks[0].HTTP, want0) {
		t.Errorf("http check = %+v", cfg.Checks[0].HTTP)
	}
	want1 := &config.HTTPRequest{On: "api", Method: "GET", Path: "/healthz", ExpectStatus: 200}
	if !reflect.DeepEqual(cfg.Checks[1].HTTP, want1) {
		t.Errorf("http check with default method = %+v", cfg.Checks[1].HTTP)
	}
	if c := cfg.Checks[2]; c.Name != "settled" || c.SQL.ExpectEmpty || c.SQL.ExpectValue != "1" {
		t.Errorf("sql expecting a value = %+v %+v", c, c.SQL)
	}
	if c := cfg.Checks[3]; c.Name != "sql-4" || c.SQL.ExpectEmpty || c.SQL.ExpectValue != "empty" {
		t.Errorf("quoted \"empty\" should be the literal text: %+v", c.SQL)
	}
	if slo := cfg.Checks[4].SLO; slo.MaxLatency != 250*time.Millisecond || slo.MaxRate != 0 {
		t.Errorf("slo on latency = %+v", slo)
	}
	if m := cfg.Checks[5].Metric; m.Min == nil || *m.Min != 1 || m.Max == nil || *m.Max != 3 || m.NoData != config.Warn {
		t.Errorf("metric = %+v", m)
	}
}

func TestSystemAndScenarioShapes(t *testing.T) {
	cfg, err := config.Parse("kazu.yaml", []byte("system: { manifests: ./k8s/rendered }\ntraffic: { image: x }\nscenarios:\n  baseline:\n  empty: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.System != (config.System{Manifests: "./k8s/rendered"}) {
		t.Errorf("system = %+v", cfg.System)
	}
	if len(cfg.Scenarios) != 2 || cfg.Scenarios[0].Name != "baseline" || cfg.Scenarios[0].Faults != nil || cfg.Scenarios[1].Faults != nil {
		t.Errorf("empty scenarios = %+v", cfg.Scenarios)
	}
	cfg = parse(t, "scenarios: { a: { traffic: { command: [] } } }\n")
	if cmd := cfg.Scenarios[0].Traffic.Command; cmd == nil || len(cmd) != 0 {
		t.Errorf("command: [] = %#v, want an empty slice (replace), not nil (keep)", cmd)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"empty file", "", "the file is empty"},
		{"comments only", "# nothing\n", "the file is empty"},
		{"second document", base + "---\nsystem: other.yml\n", "more than one YAML document"},
		{"invalid yaml", "system: [\n", "kazu.yaml: "},
		{"scalar root", "hello\n", "kazu.yaml: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("kazu.yaml", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
		})
	}
	if _, err := config.Load("testdata/missing.yaml"); err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("Load of a missing file = %v", err)
	}
}

// YAML anchors and merge keys mean what they mean in compose files.
func TestAnchorsAndMerges(t *testing.T) {
	_, err := config.Parse("kazu.yaml", []byte(base+`
x-fault: &slow { fault: latency, delay: 1s, after: 20s }
scenarios:
  a:
    faults:
      - { <<: *slow, edge: checkout -> payments }
`))
	// x- keys are compose's extension convention; Kazu doesn't take them.
	if err == nil || !strings.Contains(err.Error(), "x-fault") {
		t.Fatalf("x-fault should be rejected by name: %v", err)
	}

	cfg := parse(t, `
scenarios:
  a:
    faults:
      - &slow { edge: checkout -> payments, fault: latency, delay: 1s, after: 20s }
      - { <<: *slow, edge: payments -> postgres, delay: 2s }
`)
	f := cfg.Scenarios[0].Faults[1]
	if f.Edge != "payments -> postgres" || f.Delay != 2*time.Second || f.After != 20*time.Second {
		t.Errorf("explicit keys win over merged: %+v", f)
	}

	cfg = parse(t, `
scenarios:
  a:
    env:
      checkout: &one { A: 1, B: 1 }
      payments: &two { B: 2, C: 2 }
      worker: { <<: [*one, *two] }
`)
	if got := cfg.Scenarios[0].Env[2].Vars; !reflect.DeepEqual(got, map[string]string{"A": "1", "B": "1", "C": "2"}) {
		t.Errorf("first source wins in a merge list: %v", got)
	}
}

func TestEnvironmentForms(t *testing.T) {
	cfg, err := config.Parse("kazu.yaml", []byte(`system: docker-compose.yml
traffic:
  image: grafana/k6:1.0.0
  environment: [FLOW=browse, RATE=50, "EMPTY=", "URL=a=b"]
scenarios:
  a:
    env: { checkout: { RETRIES: 3, FAST: true, SCALE: 1.50 } }
    traffic: { environment: { RATE: 10 } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"FLOW": "browse", "RATE": "50", "EMPTY": "", "URL": "a=b"}; !reflect.DeepEqual(cfg.Traffic.Environment, want) {
		t.Errorf("list environment = %v", cfg.Traffic.Environment)
	}
	s := cfg.Scenarios[0]
	if want := map[string]string{"RETRIES": "3", "FAST": "true", "SCALE": "1.50"}; !reflect.DeepEqual(s.Env[0].Vars, want) {
		t.Errorf("scenario env keeps written text = %v", s.Env[0].Vars)
	}
	if s.Traffic.Command != nil || s.Traffic.Environment["RATE"] != "10" {
		t.Errorf("traffic override = %+v", s.Traffic)
	}
}
