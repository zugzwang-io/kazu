package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// decoder builds a Config from a tree the schema has accepted. It only
// translates: it keeps every entry, in file order, and leaves judging the
// result to validate. The schema fixes every scalar's shape, so the only
// values it can still reject are numbers YAML writes in a form the schema
// can't tell apart (200.0 for an integer) or that overflow their Go type.
type decoder struct {
	cfg  *Config
	errs []error
}

func (d *decoder) config(root *yaml.Node) (*Config, error) {
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
		d.cfg.AutoScenarios = d.boolean(n)
	}
	for _, kv := range pairs(top["scenarios"]) {
		d.cfg.Scenarios = append(d.cfg.Scenarios, d.scenario(kv.k.Value, kv.v))
	}
	if n := top["defaults"]; n != nil {
		d.defaults(n)
	}
	if n := top["checks"]; n != nil {
		for i, cn := range n.Content {
			d.cfg.Checks = append(d.cfg.Checks, d.check(i, cn))
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
	return d.cfg, errors.Join(d.errs...)
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

func (d *decoder) scenario(name string, n *yaml.Node) Scenario {
	s := Scenario{Name: name}
	f := fields(n)
	if faults := f["faults"]; faults != nil {
		for _, fn := range faults.Content {
			s.Faults = append(s.Faults, d.fault(fn))
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
		s.Duration = d.duration(v)
	}
	if v := f["not_exercised"]; v != nil {
		s.NotExercised = Severity(v.Value)
	}
	return s
}

func (d *decoder) fault(n *yaml.Node) Fault {
	f := fields(n)
	ft := Fault{Kind: FaultKind(f["fault"].Value)}
	if v := f["edge"]; v != nil {
		ft.Edge = canonicalEdge(v.Value)
	}
	if v := f["service"]; v != nil {
		ft.Service = v.Value
	}
	if v := f["delay"]; v != nil {
		ft.Delay = d.duration(v)
	}
	if v := f["jitter"]; v != nil {
		ft.Jitter = d.duration(v)
	}
	if v := f["rate"]; v != nil {
		ft.Rate = d.percent(v)
	}
	if v := f["code"]; v != nil {
		ft.Code = strings.TrimSpace(v.Value)
	}
	if v := f["limit"]; v != nil {
		ft.Limit = d.bitRate(v)
	}
	if v := f["load"]; v != nil {
		ft.Load = d.percent(v)
	}
	if v := f["restart_after"]; v != nil {
		ft.RestartAfter = d.duration(v)
	}
	if v := f["after"]; v != nil {
		ft.After = d.duration(v)
	}
	if v := f["after_requests"]; v != nil {
		ft.AfterRequests = d.integer(v)
	}
	if v := f["for"]; v != nil {
		ft.For = d.duration(v)
	}
	return ft
}

func (d *decoder) defaults(n *yaml.Node) {
	f := fields(n)
	df := &d.cfg.Defaults
	if v := f["latency_within"]; v != nil {
		df.LatencyWithin = d.percent(v)
	}
	if v := f["error_rate_within"]; v != nil {
		df.ErrorRateWithin = PercentagePoints(d.number(v, "pp"))
	}
	if v := f["recovers_within"]; v != nil {
		df.RecoversWithin = d.duration(v)
	}
	if v := f["languages"]; v != nil {
		for _, s := range strs(v) {
			df.Languages = append(df.Languages, Language(s))
		}
	}
	if v := f["off"]; v != nil {
		for _, s := range strs(v) {
			df.Off = append(df.Off, DefaultCheck(s))
		}
	}
}

// checkKinds is the order kinds are looked for in a check entry.
var checkKinds = []CheckKind{ImageCheck, SQLCheck, HTTPCheck, RecoversCheck, NoRegressionCheck, SLOCheck, MetricCheck}

// check decodes the i'th entry of the checks list; the schema guarantees
// exactly one kind key.
func (d *decoder) check(i int, n *yaml.Node) Check {
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
		if e := bf["expect"]; e.Value == "empty" && !quoted(e) {
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
			h.ExpectStatus = d.integer(s)
		}
		if b := ef["body"]; b != nil {
			h.ExpectBody = b.Value
		}
		c.HTTP = h
	case RecoversCheck:
		c.Recovers = &Recovers{Service: bf["service"].Value, Within: d.duration(bf["within"])}
	case NoRegressionCheck:
		c.NoRegression = &NoRegression{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value), Within: d.percent(bf["within"])}
	case SLOCheck:
		s := &SLO{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value)}
		if s.Measure == ErrorRate {
			s.MaxRate = d.percent(bf["max"])
		} else {
			s.MaxLatency = d.duration(bf["max"])
		}
		c.SLO = s
	case MetricCheck:
		m := &Metric{Query: bf["query"].Value}
		if v := bf["max"]; v != nil {
			x := d.float(v)
			m.Max = &x
		}
		if v := bf["min"]; v != nil {
			x := d.float(v)
			m.Min = &x
		}
		if v := bf["within"]; v != nil {
			p := d.percent(v)
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
		g.Trials = d.integer(v)
	}
	for key, dst := range map[string]*Severity{"flaky": &g.Flaky, "not_exercised": &g.NotExercised, "no_data": &g.NoData, "underpowered": &g.Underpowered} {
		if v := f[key]; v != nil {
			*dst = Severity(v.Value)
		}
	}
}

// Scalar readers. Each records a problem at the node instead of returning a
// zero that would read as a legitimate value.

func (d *decoder) fail(n *yaml.Node, msg string) {
	d.errs = append(d.errs, fmt.Errorf("line %d:%d: %s", n.Line, n.Column, msg))
}

func (d *decoder) boolean(n *yaml.Node) bool {
	var b bool
	if err := n.Decode(&b); err != nil {
		d.fail(n, "expected true or false, got "+n.Value)
	}
	return b
}

// integer reads a whole number as YAML resolves it (0x10 is 16), rejecting
// floats the schema can't distinguish from integers (200.0).
func (d *decoder) integer(n *yaml.Node) int {
	if n.ShortTag() != "!!int" {
		d.fail(n, "expected a whole number, got "+n.Value)
		return 0
	}
	var i int
	if err := n.Decode(&i); err != nil {
		d.fail(n, n.Value+" is too large a number")
	}
	return i
}

func (d *decoder) float(n *yaml.Node) float64 {
	var f float64
	if err := n.Decode(&f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		d.fail(n, "expected a number, got "+n.Value)
	}
	return f
}

func (d *decoder) duration(n *yaml.Node) time.Duration {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		d.fail(n, n.Value+" is not a duration Kazu can hold")
	}
	return v
}

func (d *decoder) percent(n *yaml.Node) Percent {
	return Percent(d.number(n, "%"))
}

// number reads a decimal written with the given unit suffix.
func (d *decoder) number(n *yaml.Node, suffix string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(n.Value, suffix), 64)
	if err != nil || math.IsInf(v, 0) {
		d.fail(n, n.Value+" is too large a number")
	}
	return v
}

var bitUnits = []struct {
	suffix string
	scale  float64
}{{"gbps", 1e9}, {"mbps", 1e6}, {"kbps", 1e3}, {"bps", 1}}

func (d *decoder) bitRate(n *yaml.Node) BitRate {
	for _, u := range bitUnits {
		if num, ok := strings.CutSuffix(n.Value, u.suffix); ok {
			x, err := strconv.ParseFloat(num, 64)
			if err != nil || x*u.scale > math.MaxInt64 {
				d.fail(n, n.Value+" is too large a bandwidth")
				return 0
			}
			return BitRate(x * u.scale)
		}
	}
	d.fail(n, n.Value+" is not a bandwidth")
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

func quoted(n *yaml.Node) bool {
	return n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0
}

// service reads a compose-style block as written. An explicit `command: []`
// is an empty slice (run the image with no arguments); absent stays nil.
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
// Values keep their written text, as compose does.
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
