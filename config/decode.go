package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// decoder builds a Config from a tree the schema has accepted, and checks
// the rules the schema can't express: references between keys, which
// fields apply to which fault, and what each protocol supports.
type decoder struct {
	ps  *problems
	cfg *Config
}

func (d *decoder) config(root *yaml.Node) *Config {
	d.cfg = &Config{
		Defaults: Defaults{LatencyWithin: 20, ErrorRateWithin: 0.5, RecoversWithin: 60 * time.Second},
		Gate:     Gate{FailOn: FailOnRegression, Flaky: Warn, NotExercised: Fail, NoData: Fail, Underpowered: Warn},
	}
	top := fields(root)
	// Edges and connections first: scenarios, checks and suites refer to them.
	if n := top["system"]; n != nil {
		d.system(n)
	}
	if n := top["traffic"]; n != nil {
		d.cfg.Traffic = service(n)
	}
	if n := top["edges"]; n != nil {
		d.edges(n)
	}
	if n := top["connections"]; n != nil {
		for _, kv := range pairs(n) {
			d.cfg.Connections = append(d.cfg.Connections, Connection{Name: kv.k.Value, URL: kv.v.Value})
		}
	}
	if n := top["telemetry"]; n != nil {
		if c := fields(n)["collector"]; c != nil {
			d.cfg.Telemetry.Collector = c.Value
		}
	}
	if n := top["auto_scenarios"]; n != nil {
		d.cfg.AutoScenarios = boolean(n)
	}
	if n := top["scenarios"]; n != nil {
		d.scenarios(n)
	}
	if n := top["defaults"]; n != nil {
		d.defaults(n)
	}
	if n := top["checks"]; n != nil {
		d.checks(n)
	}
	if n := top["suites"]; n != nil {
		d.suites(n)
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

func (d *decoder) edges(n *yaml.Node) {
	seen := map[string]bool{}
	for _, kv := range pairs(n) {
		path := []string{"edges", kv.k.Value}
		caller, callee := splitEdge(kv.k.Value)
		e := Edge{Caller: caller, Callee: callee}
		if kv.v.Kind == yaml.ScalarNode {
			e.Protocol = Protocol(kv.v.Value)
		} else {
			f := fields(kv.v)
			e.Protocol = Protocol(f["protocol"].Value)
			if t := f["tls"]; t != nil {
				tf := fields(t)
				e.TLS = &TLS{CACert: tf["ca_cert"].Value, CAKey: tf["ca_key"].Value}
				if !e.Protocol.RequestLevel() {
					d.ps.add(append(path, "tls"), "tls applies to http and grpc edges, where the proxy terminates TLS for request-level faults; network faults on a tcp edge need no certificates")
				}
			}
		}
		switch {
		case caller == callee:
			d.ps.addKey(path, fmt.Sprintf("an edge needs two different services; %s calls itself", caller))
			continue
		case callee == Traffic:
			d.ps.addKey(path, "traffic only sends requests, so nothing calls it; edges into traffic aren't allowed")
			continue
		case seen[e.Name()]:
			d.ps.addKey(path, fmt.Sprintf("%s is declared twice", e.Name()))
			continue
		}
		seen[e.Name()] = true
		d.cfg.Edges = append(d.cfg.Edges, e)
	}
}

func splitEdge(s string) (caller, callee string) {
	a, b, _ := strings.Cut(s, "->")
	return strings.TrimSpace(a), strings.TrimSpace(b)
}

func canonicalEdge(s string) string {
	a, b := splitEdge(s)
	return a + " -> " + b
}

func (d *decoder) scenarios(n *yaml.Node) {
	for _, kv := range pairs(n) {
		name := kv.k.Value
		path := []string{"scenarios", name}
		if name == "auto" || name == "all" || strings.HasPrefix(name, "auto-") {
			d.ps.addKey(path, fmt.Sprintf("%q is reserved: \"all\" and \"auto\" are suite keywords, and names starting with \"auto-\" belong to generated scenarios", name))
			continue
		}
		s := Scenario{Name: name}
		f := fields(kv.v)
		if faults := f["faults"]; faults != nil {
			for i, fn := range faults.Content {
				if fault, ok := d.fault(append(slices.Clone(path), "faults", strconv.Itoa(i)), fn); ok {
					s.Faults = append(s.Faults, fault)
				}
			}
		}
		if env := f["env"]; env != nil {
			for _, svc := range pairs(env) {
				s.Env = append(s.Env, ServiceEnv{Service: svc.k.Value, Vars: environment(svc.v)})
			}
		}
		if t := f["traffic"]; t != nil {
			svc := service(t)
			s.Traffic = &svc
		}
		if dn := f["duration"]; dn != nil {
			s.Duration = d.positive(append(slices.Clone(path), "duration"), dn)
		}
		if ne := f["not_exercised"]; ne != nil {
			s.NotExercised = Severity(ne.Value)
		}
		d.cfg.Scenarios = append(d.cfg.Scenarios, s)
	}
}

// faultFields lists the fields each kind takes besides the target and
// timing fields (after, after_requests, for).
var faultFields = map[FaultKind][]string{
	Latency:   {"delay", "jitter"},
	Down:      {},
	Blackhole: {},
	Reset:     {"rate"},
	Bandwidth: {"limit"},
	Errors:    {"rate", "code"},
	Timeout:   {"rate"},
	Crash:     {"restart_after"},
	Pause:     {},
	CPU:       {"load"},
	Memory:    {"load"},
	DiskFull:  {},
}

var timingFields = []string{"after", "after_requests", "for"}

func (d *decoder) fault(path []string, n *yaml.Node) (Fault, bool) {
	f := fields(n)
	ft := Fault{Kind: FaultKind(f["fault"].Value)}
	ok := true
	at := func(key string) []string { return append(slices.Clone(path), key) }
	bad := func(key, msg string) {
		d.ps.add(at(key), msg)
		ok = false
	}

	// Fields that don't apply to this kind.
	allowed := append(slices.Clone(faultFields[ft.Kind]), timingFields...)
	allowed = append(allowed, "fault", "edge", "service")
	for _, kv := range pairs(n) {
		if !slices.Contains(allowed, kv.k.Value) {
			d.ps.addKey(at(kv.k.Value), fmt.Sprintf("%s doesn't apply to %s fault", kv.k.Value, article(ft.Kind)))
			ok = false
		}
	}

	// Target.
	edgeN, svcN := f["edge"], f["service"]
	var edge Edge
	switch {
	case edgeN != nil && svcN != nil:
		d.ps.add(path, "set either edge or service, not both")
		return ft, false
	case edgeN == nil && svcN == nil:
		d.ps.add(path, "missing a target: set edge (one declared edge) or service")
		return ft, false
	case edgeN != nil:
		ft.Edge = canonicalEdge(edgeN.Value)
		e, found := d.cfg.Edge(ft.Edge)
		if !found {
			bad("edge", fmt.Sprintf("%s is not a declared edge; faults go only on edges listed under edges", ft.Edge)+suggest(ft.Edge, d.edgeNames()))
			return ft, false
		}
		edge = e
	default:
		ft.Service = svcN.Value
	}

	// What the target supports.
	layer := ft.Kind.Layer()
	switch {
	case layer == Request && ft.Service != "":
		bad("service", fmt.Sprintf("%s fault goes on one http or grpc edge; use edge: <caller> -> %s", article(ft.Kind), ft.Service))
	case layer == Request && !edge.Protocol.RequestLevel():
		bad("edge", fmt.Sprintf("%s fault needs an http or grpc edge; %s is %s, so the proxy sees bytes, not requests", article(ft.Kind), ft.Edge, edge.Protocol))
	case layer == Process && ft.Edge != "":
		bad("edge", fmt.Sprintf("%s fault acts on a service's container; use service: %s", article(ft.Kind), edge.Callee))
	case ft.Service == Traffic:
		bad("service", "traffic is the load, not part of the system; faults go on the system's services and edges")
	case layer == Network && ft.Service != "" && len(d.cfg.Inbound(ft.Service)) == 0:
		bad("service", fmt.Sprintf("%s fault on a service applies to its declared inbound edges, and %s has none; declare an edge into %s", article(ft.Kind), ft.Service, ft.Service))
	}

	// Kind-specific fields.
	if v := f["delay"]; v != nil {
		ft.Delay = d.positive(at("delay"), v)
	}
	if v := f["jitter"]; v != nil {
		ft.Jitter = duration(v)
	}
	if v := f["rate"]; v != nil {
		ft.Rate = d.share(at("rate"), v)
	}
	if v := f["load"]; v != nil {
		ft.Load = d.share(at("load"), v)
	}
	if v := f["limit"]; v != nil {
		ft.Limit = d.bitRate(at("limit"), v)
	}
	if v := f["restart_after"]; v != nil {
		ft.RestartAfter = duration(v)
	}
	if v := f["code"]; v != nil && edge.Protocol.RequestLevel() {
		code, err := statusCode(edge.Protocol, v)
		if err != "" {
			bad("code", err)
		}
		ft.Code = code
	}

	// Timing.
	if v := f["for"]; v != nil {
		if ft.Kind == Crash {
			bad("for", "a crash is instant; restart_after sets how long the service stays down")
		}
		ft.For = d.positive(at("for"), v)
	}
	afterN, countN := f["after"], f["after_requests"]
	if afterN != nil && countN != nil {
		d.ps.add(path, "set after or after_requests, not both")
		ok = false
	}
	if afterN != nil {
		ft.After = duration(afterN)
	}
	if countN != nil {
		ft.AfterRequests, _ = strconv.Atoi(countN.Value)
		d.requestTrigger(at("after_requests"), ft, edge, &ok)
	}
	return ft, ok
}

// requestTrigger checks that an after_requests trigger has requests to
// count: the proxy parses requests only on http and grpc edges.
func (d *decoder) requestTrigger(path []string, ft Fault, edge Edge, ok *bool) {
	if ft.Edge != "" {
		if !edge.Protocol.RequestLevel() {
			d.ps.add(path, fmt.Sprintf("%s is a %s edge: its proxy sees bytes, not requests, so there are none to count. Use after: <duration>, or declare the edge http or grpc if it carries those", ft.Edge, edge.Protocol))
			*ok = false
		}
		return
	}
	var counted []string
	for _, e := range d.cfg.Inbound(ft.Service) {
		if e.Protocol.RequestLevel() {
			counted = append(counted, e.Name())
		}
	}
	if len(counted) == 0 {
		d.ps.add(path, fmt.Sprintf("after_requests on a service counts requests on its declared http and grpc inbound edges, and %s has none. Use after: <duration>", ft.Service))
		*ok = false
	}
}

// article prefixes a fault kind with "a" or "an".
func article(k FaultKind) string {
	if strings.ContainsRune("aeiou", rune(k[0])) {
		return "an " + string(k)
	}
	return "a " + string(k)
}

// grpcCodes are the gRPC status code names, in code order.
var grpcCodes = []string{
	"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
	"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
	"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
	"UNAUTHENTICATED",
}

// statusCode returns the canonical code for an errors fault: an HTTP status
// as digits, or a gRPC code name. A non-empty second value is the problem.
func statusCode(p Protocol, n *yaml.Node) (string, string) {
	v := strings.TrimSpace(n.Value)
	if p == HTTP {
		c, err := strconv.Atoi(v)
		if err != nil || c < 100 || c > 599 {
			return "", fmt.Sprintf("%q is not an HTTP status; use a number from 100 to 599, such as 503", v)
		}
		return strconv.Itoa(c), ""
	}
	if c, err := strconv.Atoi(v); err == nil {
		if c < 0 || c >= len(grpcCodes) {
			return "", fmt.Sprintf("%d is not a gRPC status code (0 to %d)", c, len(grpcCodes)-1)
		}
		return grpcCodes[c], ""
	}
	up := strings.ToUpper(v)
	if slices.Contains(grpcCodes, up) {
		return up, ""
	}
	return "", fmt.Sprintf("%q is not a gRPC status code, such as UNAVAILABLE", v) + suggest(up, grpcCodes)
}

func (d *decoder) defaults(n *yaml.Node) {
	f := fields(n)
	df := &d.cfg.Defaults
	if v := f["latency_within"]; v != nil {
		df.LatencyWithin = percent(v)
	}
	if v := f["error_rate_within"]; v != nil {
		df.ErrorRateWithin, _ = strconv.ParseFloat(strings.TrimSuffix(v.Value, "pp"), 64)
		if df.ErrorRateWithin > 100 {
			d.ps.add([]string{"defaults", "error_rate_within"}, "can't exceed 100pp")
		}
	}
	if v := f["recovers_within"]; v != nil {
		df.RecoversWithin = d.positive([]string{"defaults", "recovers_within"}, v)
	}
	if v := f["languages"]; v != nil {
		df.Languages = strs(v)
	}
	if v := f["off"]; v != nil {
		df.Off = strs(v)
	}
}

func (d *decoder) checks(n *yaml.Node) {
	kinds := []CheckKind{ImageCheck, SQLCheck, HTTPCheck, RecoversCheck, NoRegressionCheck, SLOCheck, MetricCheck}
	names := map[string]bool{}
	for i, cn := range n.Content {
		path := []string{"checks", strconv.Itoa(i)}
		at := func(key string) []string { return append(slices.Clone(path), key) }
		f := fields(cn)
		var found []CheckKind
		for _, k := range kinds {
			if f[string(k)] != nil {
				found = append(found, k)
			}
		}
		if len(found) != 1 {
			if len(found) == 0 {
				d.ps.add(path, "a check needs one of image, sql, http, recovers, no_regression, slo or metric")
			} else {
				d.ps.add(path, fmt.Sprintf("a check has exactly one kind; this one has %s", strings.Join(checkKindNames(found), " and ")))
			}
			continue
		}
		c := Check{Kind: found[0]}
		if c.Kind != ImageCheck {
			for _, key := range []string{"command", "volumes", "environment"} {
				if f[key] != nil {
					d.ps.addKey(at(key), fmt.Sprintf("%s belongs to a code check (an image: entry), not a %s check", key, c.Kind))
				}
			}
		}
		if v := f["name"]; v != nil {
			c.Name = v.Value
		} else {
			c.Name = fmt.Sprintf("%s-%d", c.Kind, i+1)
		}
		if names[c.Name] {
			d.ps.add(at("name"), fmt.Sprintf("another check is already named %q", c.Name))
		}
		names[c.Name] = true

		body := f[string(c.Kind)]
		bodyPath := at(string(c.Kind))
		bf := fields(body)
		switch c.Kind {
		case ImageCheck:
			svc := service(cn)
			c.Image = &svc
		case SQLCheck:
			s := &SQL{On: bf["on"].Value, Query: bf["query"].Value}
			if e := bf["expect"]; e.Value == "empty" && e.ShortTag() == "!!str" {
				s.ExpectEmpty = true
			} else {
				s.ExpectValue = e.Value
			}
			d.connection(append(slices.Clone(bodyPath), "on"), s.On)
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
			d.connection(append(slices.Clone(bodyPath), "on"), h.On)
			c.HTTP = h
		case RecoversCheck:
			r := &Recovers{Service: bf["service"].Value}
			r.Within = d.positive(append(slices.Clone(bodyPath), "within"), bf["within"])
			d.measured(append(slices.Clone(bodyPath), "service"), r.Service)
			c.Recovers = r
		case NoRegressionCheck:
			r := &NoRegression{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value), Within: percent(bf["within"])}
			d.measured(append(slices.Clone(bodyPath), "service"), r.Service)
			c.NoRegression = r
		case SLOCheck:
			s := &SLO{Service: bf["service"].Value, Measure: Measure(bf["measure"].Value)}
			d.slo(bodyPath, s, bf["max"])
			d.measured(append(slices.Clone(bodyPath), "service"), s.Service)
			c.SLO = s
		case MetricCheck:
			c.Metric = d.metric(bodyPath, bf)
		}
		d.cfg.Checks = append(d.cfg.Checks, c)
	}
}

func checkKindNames(ks []CheckKind) []string {
	s := make([]string, len(ks))
	for i, k := range ks {
		s[i] = string(k)
	}
	return s
}

func (d *decoder) connection(path []string, name string) {
	var names []string
	for _, c := range d.cfg.Connections {
		if c.Name == name {
			return
		}
		names = append(names, c.Name)
	}
	msg := fmt.Sprintf("no connection named %q; declare it under connections", name)
	if hint := suggest(name, names); hint != "" {
		msg += hint
	} else if len(names) > 0 {
		msg += " (declared: " + strings.Join(names, ", ") + ")"
	}
	d.ps.add(path, msg)
}

// measured checks that a service has a declared inbound edge whose proxy
// measures requests, which recovers, no_regression and slo read.
func (d *decoder) measured(path []string, service string) {
	in := d.cfg.Inbound(service)
	for _, e := range in {
		if e.Protocol.RequestLevel() {
			return
		}
	}
	if len(in) == 0 {
		var callees []string
		for _, e := range d.cfg.Edges {
			callees = append(callees, e.Callee)
		}
		d.ps.add(path, fmt.Sprintf("%s has no declared inbound edges, so no proxy measures it; declare an http or grpc edge into %s", service, service)+suggest(service, callees))
		return
	}
	d.ps.add(path, fmt.Sprintf("%s's declared inbound edges are all tcp, where the proxy sees bytes, not requests; this check needs an http or grpc edge into %s", service, service))
}

func (d *decoder) slo(path []string, s *SLO, maxN *yaml.Node) {
	mp := append(slices.Clone(path), "max")
	if s.Measure == ErrorRate {
		if !strings.HasSuffix(maxN.Value, "%") {
			d.ps.add(mp, fmt.Sprintf("%q is not a percentage; error_rate takes one such as 1%%", maxN.Value))
			return
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(maxN.Value, "%"), 64)
		if err != nil || v < 0 || v > 100 {
			d.ps.add(mp, fmt.Sprintf("%q is not a percentage from 0%% to 100%%", maxN.Value))
			return
		}
		s.MaxRate = Percent(v)
		return
	}
	v, err := time.ParseDuration(maxN.Value)
	if err != nil || v <= 0 {
		d.ps.add(mp, fmt.Sprintf("%q is not a duration; %s takes one such as 250ms", maxN.Value, s.Measure))
		return
	}
	s.MaxLatency = v
}

func (d *decoder) metric(path []string, f map[string]*yaml.Node) *Metric {
	m := &Metric{Query: f["query"].Value}
	if v := f["max"]; v != nil {
		x, _ := strconv.ParseFloat(v.Value, 64)
		m.Max = &x
	}
	if v := f["min"]; v != nil {
		x, _ := strconv.ParseFloat(v.Value, 64)
		m.Min = &x
	}
	if v := f["within"]; v != nil {
		p := percent(v)
		m.Within = &p
	}
	if v := f["no_data"]; v != nil {
		m.NoData = Severity(v.Value)
	}
	switch {
	case m.Within != nil && (m.Max != nil || m.Min != nil):
		d.ps.add(path, "set within (relative to base) or max/min (absolute), not both")
	case m.Within == nil && m.Max == nil && m.Min == nil:
		d.ps.add(path, "a metric check needs a bound: max, min, or within (relative to base)")
	case m.Max != nil && m.Min != nil && *m.Min > *m.Max:
		d.ps.add(path, fmt.Sprintf("min (%g) is above max (%g), so no value can pass", *m.Min, *m.Max))
	}
	return m
}

func (d *decoder) suites(n *yaml.Node) {
	for _, kv := range pairs(n) {
		s := Suite{Name: kv.k.Value}
		path := []string{"suites", s.Name}
		if kv.v.Kind == yaml.ScalarNode {
			s.All = true
			d.cfg.Suites = append(d.cfg.Suites, s)
			continue
		}
		for i, item := range kv.v.Content {
			d.suiteEntry(append(slices.Clone(path), strconv.Itoa(i)), item.Value)
			s.Scenarios = append(s.Scenarios, item.Value)
		}
		d.cfg.Suites = append(d.cfg.Suites, s)
	}
}

func (d *decoder) suiteEntry(path []string, name string) {
	if _, ok := d.cfg.Scenario(name); ok {
		return
	}
	isAuto := name == "auto" || strings.HasPrefix(name, "auto-")
	if isAuto && !d.cfg.AutoScenarios {
		d.ps.add(path, fmt.Sprintf("%s is a generated scenario; set auto_scenarios: true to generate them", name))
		return
	}
	switch {
	case name == "auto", strings.HasPrefix(name, "auto-crash-") && len(name) > len("auto-crash-"):
		// Services come from the system file, which the driver reads.
		return
	case strings.HasPrefix(name, "auto-down-"):
		for _, e := range d.cfg.Edges {
			if !e.UserFacing() && name == "auto-down-"+e.Caller+"-"+e.Callee {
				return
			}
		}
	}
	names := []string{}
	for _, s := range d.cfg.Scenarios {
		names = append(names, s.Name)
	}
	if d.cfg.AutoScenarios {
		for _, e := range d.cfg.Edges {
			if !e.UserFacing() {
				names = append(names, "auto-down-"+e.Caller+"-"+e.Callee)
			}
		}
	}
	d.ps.add(path, fmt.Sprintf("no scenario named %q", name)+suggest(name, names))
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

func (d *decoder) edgeNames() []string {
	names := make([]string, len(d.cfg.Edges))
	for i, e := range d.cfg.Edges {
		names[i] = e.Name()
	}
	return names
}

// positive parses a duration that must be above zero.
func (d *decoder) positive(path []string, n *yaml.Node) time.Duration {
	v := duration(n)
	if v <= 0 {
		d.ps.add(path, "must be longer than zero")
	}
	return v
}

// share parses a percentage of something: above 0% and at most 100%.
func (d *decoder) share(path []string, n *yaml.Node) Percent {
	p := percent(n)
	if p <= 0 || p > 100 {
		d.ps.add(path, fmt.Sprintf("%s is outside 0%% to 100%% (zero would never fire)", n.Value))
	}
	return p
}

var bitUnits = map[string]float64{"gbps": 1e9, "mbps": 1e6, "kbps": 1e3, "bps": 1}

func (d *decoder) bitRate(path []string, n *yaml.Node) BitRate {
	for _, u := range []string{"gbps", "mbps", "kbps", "bps"} {
		if num, ok := strings.CutSuffix(n.Value, u); ok {
			x, _ := strconv.ParseFloat(num, 64)
			r := BitRate(x * bitUnits[u])
			if r <= 0 {
				d.ps.add(path, "must be above zero; use down to stop traffic entirely")
			}
			return r
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
