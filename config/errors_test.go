package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zugzwang-io/kazu/config"
)

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
		{"unknown fault kind", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latncy, delay: 1s }\n", []string{
			`13:46: scenarios.a.faults[0].fault: "latncy" is not one of "latency", "down", "blackhole", "reset", "bandwidth", "errors", "timeout", "crash", "pause", "cpu", "memory", "disk_full" (did you mean "latency"?)`,
		}},
		{"missing required field", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: latency }\n", []string{
			`13:9: scenarios.a.faults[0]: missing key "delay"`,
		}},
		{"duration without a unit", "scenarios:\n  a:\n    faults:\n      - { edge: checkout -> payments, fault: down, after: 20 }\n", []string{
			`13:59: scenarios.a.faults[0].after: expected a duration such as 300ms, 20s or 1m30s, got a number`,
		}},
		{"empty suite", "suites:\n  pr: []\n", []string{
			`11:7: suites.pr: must not be empty`,
		}},
		{"gate", "gate: { trials: sometimes, flaky: maybe }\n", []string{
			`10:17: gate.trials: "sometimes" is not allowed here; the only text value is "auto"`,
			`10:35: gate.flaky: "maybe" is not one of "fail", "warn"`,
		}},
		{"key that is not a name", "scenarios:\n  a: { faults: [] }\n  b:\n    env:\n      checkout payments: { A: b }\n", []string{
			`14:7: scenarios.b.env["checkout payments"]: "checkout payments" is not a name (letters, digits, '.', '_' and '-')`,
		}},
		{"invalid YAML", "x: [\n", []string{
			`10:1: invalid YAML: did not find expected node content`,
		}},
		{"duplicate key", "scenarios:\n  a: {}\n  a: {}\n", []string{
			`12:3: key "a" appears twice in the same map`,
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
