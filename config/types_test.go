package config_test

import (
	"testing"

	"github.com/zugzwang-io/kazu/config"
)

// Every fault kind has a layer: network, request or process.
func TestFaultKindLayer(t *testing.T) {
	want := map[config.FaultKind]config.Layer{
		config.Latency: config.Network, config.Down: config.Network, config.Blackhole: config.Network,
		config.Reset: config.Network, config.Bandwidth: config.Network,
		config.Errors: config.Request, config.Timeout: config.Request,
		config.Crash: config.Process, config.Pause: config.Process, config.CPU: config.Process,
		config.Memory: config.Process, config.DiskFull: config.Process,
	}
	for kind, layer := range want {
		if got := kind.Layer(); got != layer {
			t.Errorf("%s: layer %v, want %v", kind, got, layer)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("unknown kind did not panic")
		}
	}()
	config.FaultKind("bogus").Layer()
}

func TestConfigLookups(t *testing.T) {
	cfg := &config.Config{
		Edges: []config.Edge{
			{Caller: "traffic", Callee: "checkout", Protocol: config.HTTP},
			{Caller: "checkout", Callee: "payments", Protocol: config.HTTP},
			{Caller: "worker", Callee: "payments", Protocol: config.TCP},
		},
		Scenarios: []config.Scenario{{Name: "db-blip"}},
	}
	if e, ok := cfg.Edge("checkout -> payments"); !ok || e.Protocol != config.HTTP {
		t.Errorf("Edge(checkout -> payments) = %+v, %v", e, ok)
	}
	if _, ok := cfg.Edge("checkout->payments"); ok {
		t.Error("Edge matched a non-canonical name; the loader owns normalisation")
	}
	if e, _ := cfg.Edge("traffic -> checkout"); !e.UserFacing() {
		t.Error("edge from traffic is not user-facing")
	}
	if in := cfg.Inbound("payments"); len(in) != 2 || in[0].Caller != "checkout" || in[1].Caller != "worker" {
		t.Errorf("Inbound(payments) = %+v, want checkout then worker", in)
	}
	if in := cfg.Inbound("nowhere"); in != nil {
		t.Errorf("Inbound(nowhere) = %+v, want nil", in)
	}
	if _, ok := cfg.Scenario("db-blip"); !ok {
		t.Error("Scenario(db-blip) not found")
	}
	if _, ok := cfg.Scenario("DB-BLIP"); ok {
		t.Error("Scenario matched case-insensitively")
	}
}
