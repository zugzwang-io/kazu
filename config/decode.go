package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// decoder builds a Config from a tree the schema has accepted. It only
// translates: it keeps every entry, in file order, and leaves judging the
// result to validate.
type decoder struct {
	cfg *Config
}

func (d *decoder) config(root *yaml.Node) *Config {
	d.cfg = &Config{
		Defaults: Defaults{LatencyWithin: 20, ErrorRateWithin: 0.5, RecoversWithin: 60 * time.Second},
		Gate:     Gate{FailOn: FailOnRegression, Flaky: Warn, NotExercised: Fail, NoData: Fail, Underpowered: Warn},
	}
	top := fields(root)
	if n := top["system"]; n != nil {
		d.system(n)
	}
	if n := top["traffic"]; n != nil {
		d.cfg.Traffic = service(n)
	}
	for _, kv := range pairs(top["edges"]) {
		d.cfg.Edges = append(d.cfg.Edges, edge(kv.k, kv.v))
	}
	for _, kv := range pairs(top["connections"]) {
		d.cfg.Connections = append(d.cfg.Connections, Connection{Name: kv.k.Value, URL: kv.v.Value})
	}
	if n := top["telemetry"]; n != nil {
		if c := fields(n)["collector"]; c != nil {
			d.cfg.Telemetry.Collector = c.Value
		}
	}
	if n := top["auto_scenarios"]; n != nil {
		d.cfg.AutoScenarios = boolean(n)
	}
	for _, kv := range pairs(top["scenarios"]) {
		d.cfg.Scenarios = append(d.cfg.Scenarios, scenario(kv.k.Value, kv.v))
	}
	if n := top["defaults"]; n != nil {
		d.defaults(n)
	}
	if n := top["checks"]; n != nil {
		for i, cn := range n.Content {
			d.cfg.Checks = append(d.cfg.Checks, check(i, cn))
		}
	}
	for _, kv := range pairs(top["suites"]) {
		s := Suite{Name: kv.k.Value}
		if kv.v.Kind == yaml.ScalarNode {
			s.All = true
		} else {
			s.Scenarios = strs(kv.v)
		}
		d.cfg.Suites = append(d.cfg.Suites, s)
	}
	if n := top["gate"]; n != nil {
		d.gate(n)
	}
	return d.cfg
}

func (d *decoder) system(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode {
		d.cfg.System.Compose = n.Value
		return
	}
	d.cfg.System.Manifests = fields(n)["manifests"].Value
}

func edge(k, v *yaml.Node) Edge {
	caller, callee := splitEdge(k.Value)
	e := Edge{Caller: caller, Callee: callee}
	if v.Kind == yaml.ScalarNode {
		e.Protocol = Protocol(v.Value)
		return e
	}
	f := fields(v)
	e.Protocol = Protocol(f["protocol"].Value)
	if t := f["tls"]; t != nil {
		tf := fields(t)
		e.TLS = &TLS{CACert: tf["ca_cert"].Value, CAKey: tf["ca_key"].Value}
	}
	return e
}

func splitEdge(s string) (caller, callee string) {
	a, b, _ := strings.Cut(s, "->")
	return strings.TrimSpace(a), strings.TrimSpace(b)
}

func canonicalEdge(s string) string {
	a, b := splitEdge(s)
	return a + " -> " + b
}

func scenario(name string, n *yaml.Node) Scenario {
	s := Scenario{Name: name}
	f := fields(n)
	if faults := f["faults"]; faults != nil {
		for _, fn := range faults.Content {
			s.Faults = append(s.Faults, fault(fn))
		}
	}
	for _, svc := range pairs(f["env"]) {
		s.Env = append(s.Env, ServiceEnv{Service: svc.k.Value, Vars: environment(svc.v)})
	}
	if t := f["traffic"]; t != nil {
		svc := service(t)
		s.Traffic = &svc
	}
	if v := f["duration"]; v != nil {
		s.Duration = duration(v)
	}
	if v := f["not_exercised"]; v != nil {
		s.NotExercised = Severity(v.Value)
	}
	return s
}

func fault(n *yaml.Node) Fault {
	f := fields(n)
	ft := Fault{Kind: FaultKind(f["fault"].Value)}
	if v := f["edge"]; v != nil {
		ft.Edge = canonicalEdge(v.Value)
	}
	if v := f["service"]; v != nil {
		ft.Service = v.Value
	}
	if v := f["delay"]; v != nil {
		ft.Delay = duration(v)
	}
	if v := f["jitter"]; v != nil {
		ft.Jitter = duration(v)
	}
	if v := f["rate"]; v != nil {
		ft.Rate = percent(v)
	}
	if v := f["code"]; v != nil {
		ft.Code = strings.TrimSpace(v.Value)
	}
	if v := f["limit"]; v != nil {
		ft.Limit = bitRate(v)
	}
	if v := f["load"]; v != nil {
		ft.Load = percent(v)
	}
	if v := f["restart_after"]; v != nil {
		ft.RestartAfter = duration(v)
	}
	if v := f["after"]; v != nil {
		ft.After = duration(v)
	}
	if v := f["after_requests"]; v != nil {
		ft.AfterRequests, _ = strconv.Atoi(v.Value)
	}
	if v := f["for"]; v != nil {
		ft.For = duration(v)
	}
	return ft
}

func (d *decoder) defaults(n *yaml.Node) {
	f := fields(n)
	df := &d.cfg.Defaults
	if v := f["latency_within"]; v != nil {
		df.LatencyWithin = percent(v)
	}
	if v := f["error_rate_within"]; v != nil {
		df.ErrorRateWithin, _ = strconv.ParseFloat(strings.TrimSuffix(v.Value, "pp"), 64)
	}
	if v := f["recovers_within"]; v != nil {
		df.RecoversWithin = duration(v)
	}
	if v := f["languages"]; v != nil {
		df.Languages = strs(v)
	}
	if v := f["off"]; v != nil {
		df.Off = strs(v)
	}
}

// checkKinds is the order kinds are looked for in a check entry.
var checkKinds = []CheckKind{ImageCheck, SQLCheck, HTTPCheck, RecoversCheck, NoRegressionCheck, SLOCheck, MetricCheck}

// check decodes the i'th entry of the checks list. Kind is the first kind
// key present, or empty when there is none.
func check(i int, n *yaml.Node) Check {
	f := fields(n)
	var c Check
	for _, k := range checkKinds {
		if f[string(k)] != nil {
			c.Kind = k
			break
		}
	}
	if v := f["name"]; v != nil {
		c.Name = v.Value
	} else {
		c.Name = fmt.Sprintf("%s-%d", c.Kind, i+1)
	}
	bf := fields(f[string(c.Kind)])
	switch c.Kind {
	case ImageCheck:
		svc := service(n)
		c.Image = &svc
	case SQLCheck:
		s := &SQL{On: bf["on"].Value, Query: bf["query"].Value}
		if e := bf["expect"]; e.Value == "empty" && e.ShortTag() == "!!str" {
			s.ExpectEmpty = true
		} else {
			s.ExpectValue = e.Value
		}
		c.SQL = s
	case HTTPCheck:
		h := &HTTPRequest{On: bf["on"].Value, Method: "GET", Path: bf["path"].Value}
		if m := bf["method"]; m != nil {
			h.Method = m.Value
		}
		ef := fields(bf["expect"])
		if s := ef["status"]; s != nil {
			h.ExpectStatus, _ = strconv.Atoi(s.Value)
		}
		if b := ef["body"]; b != nil {
			h.ExpectBody = b.Value
		}
		c.HTTP = h
	case RecoversCheck:
		c.Recovers = &Recovers{Service: bf["service"].Value, Within: duration(bf["within"])}
	case NoRegressionCheck:
		c.NoRegression = &NoRegression{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value), Within: percent(bf["within"])}
	case SLOCheck:
		s := &SLO{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value)}
		maxV := bf["max"].Value
		if s.Measure == ErrorRate {
			if v, ok := strings.CutSuffix(maxV, "%"); ok {
				p, _ := strconv.ParseFloat(v, 64)
				s.MaxRate = Percent(p)
			}
		} else {
			s.MaxLatency, _ = time.ParseDuration(maxV)
		}
		c.SLO = s
	case MetricCheck:
		m := &Metric{Query: bf["query"].Value}
		if v := bf["max"]; v != nil {
			x, _ := strconv.ParseFloat(v.Value, 64)
			m.Max = &x
		}
		if v := bf["min"]; v != nil {
			x, _ := strconv.ParseFloat(v.Value, 64)
			m.Min = &x
		}
		if v := bf["within"]; v != nil {
			p := percent(v)
			m.Within = &p
		}
		if v := bf["no_data"]; v != nil {
			m.NoData = Severity(v.Value)
		}
		c.Metric = m
	default:
	}
	return c
}

func (d *decoder) gate(n *yaml.Node) {
	f := fields(n)
	g := &d.cfg.Gate
	if v := f["fail_on"]; v != nil {
		g.FailOn = FailOn(v.Value)
	}
	if v := f["trials"]; v != nil && v.Value != "auto" {
		g.Trials, _ = strconv.Atoi(v.Value)
	}
	for key, dst := range map[string]*Severity{"flaky": &g.Flaky, "not_exercised": &g.NotExercised, "no_data": &g.NoData, "underpowered": &g.Underpowered} {
		if v := f[key]; v != nil {
			*dst = Severity(v.Value)
		}
	}
}

var bitUnits = []struct {
	suffix string
	scale  float64
}{{"gbps", 1e9}, {"mbps", 1e6}, {"kbps", 1e3}, {"bps", 1}}

func bitRate(n *yaml.Node) BitRate {
	for _, u := range bitUnits {
		if num, ok := strings.CutSuffix(n.Value, u.suffix); ok {
			x, _ := strconv.ParseFloat(num, 64)
			return BitRate(x * u.scale)
		}
	}
	return 0
}

// The helpers below read nodes the schema has already shaped.

type pair struct{ k, v *yaml.Node }

func pairs(n *yaml.Node) []pair {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	ps := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		ps = append(ps, pair{n.Content[i], n.Content[i+1]})
	}
	return ps
}

func fields(n *yaml.Node) map[string]*yaml.Node {
	m := map[string]*yaml.Node{}
	for _, kv := range pairs(n) {
		m[kv.k.Value] = kv.v
	}
	return m
}

func service(n *yaml.Node) Service {
	f := fields(n)
	var s Service
	if v := f["image"]; v != nil {
		s.Image = v.Value
	}
	if v := f["command"]; v != nil {
		s.Command = strs(v)
		if s.Command == nil {
			s.Command = []string{}
		}
	}
	if v := f["volumes"]; v != nil {
		s.Volumes = strs(v)
		if s.Volumes == nil {
			s.Volumes = []string{}
		}
	}
	if v := f["environment"]; v != nil {
		s.Environment = environment(v)
	}
	return s
}

// environment reads a compose environment: a map, or a list of NAME=value.
func environment(n *yaml.Node) map[string]string {
	env := map[string]string{}
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			k, v, _ := strings.Cut(item.Value, "=")
			env[k] = v
		}
		return env
	}
	for _, kv := range pairs(n) {
		env[kv.k.Value] = kv.v.Value
	}
	return env
}

func strs(n *yaml.Node) []string {
	var s []string
	for _, c := range n.Content {
		s = append(s, c.Value)
	}
	return s
}

func boolean(n *yaml.Node) bool {
	var b bool
	_ = n.Decode(&b)
	return b
}

func duration(n *yaml.Node) time.Duration {
	v, _ := time.ParseDuration(n.Value)
	return v
}

func percent(n *yaml.Node) Percent {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(n.Value, "%"), 64)
	return Percent(v)
}
