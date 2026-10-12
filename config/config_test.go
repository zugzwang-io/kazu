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
	// x- keys are compose's extension convention; Kazu doesn't take them.
	if err == nil || cfg != nil {
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
