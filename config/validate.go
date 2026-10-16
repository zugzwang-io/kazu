package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// validator checks the rules that need the whole config: references between
// keys, what each edge's protocol supports, and values in range. Rules that
// hold inside one object (which fields a fault kind takes, one check kind)
// are the schema's. It reads the decoded Config, and the tree it came from
// for positions and for which keys were written.
type validator struct {
	ps   *problems
	root *yaml.Node
	cfg  *Config
}

func validate(ps *problems, root *yaml.Node, cfg *Config) {
	v := validator{ps: ps, root: root, cfg: cfg}
	v.edges()
	for _, s := range cfg.Scenarios {
		v.scenario(s)
	}
	v.defaults()
	v.checks()
	for _, s := range cfg.Suites {
		for i, name := range s.Scenarios {
			v.suiteEntry([]string{"suites", s.Name, strconv.Itoa(i)}, name)
		}
	}
}

// node returns the node at path, or nil.
func (v *validator) node(path ...string) *yaml.Node {
	n := v.root
	for _, tok := range path {
		if n = child(n, tok); n == nil {
			return nil
		}
	}
	return n
}

func (v *validator) edges() {
	seen := map[string]bool{}
	for i, kv := range pairs(v.node("edges")) {
		e := v.cfg.Edges[i]
		path := []string{"edges", kv.k.Value}
		if e.TLS != nil && !e.Protocol.RequestLevel() {
			v.ps.add(append(path, "tls"), "tls applies to http and grpc edges, where the proxy terminates TLS for request-level faults; network faults on a tcp edge need no certificates")
		}
		switch {
		case e.Caller == e.Callee:
			v.ps.addKey(path, fmt.Sprintf("an edge needs two different services; %s calls itself", e.Caller))
		case e.Callee == Traffic:
			v.ps.addKey(path, "traffic only sends requests, so nothing calls it; edges into traffic aren't allowed")
		case seen[e.Name()]:
			v.ps.addKey(path, fmt.Sprintf("%s is declared twice", e.Name()))
		}
		seen[e.Name()] = true
	}
}

func (v *validator) scenario(s Scenario) {
	path := []string{"scenarios", s.Name}
	if s.Name == "auto" || s.Name == "all" || strings.HasPrefix(s.Name, "auto-") {
		v.ps.addKey(path, fmt.Sprintf("%q is reserved: \"all\" and \"auto\" are suite keywords, and names starting with \"auto-\" belong to generated scenarios", s.Name))
	}
	for i := range s.Faults {
		v.fault(append(slices.Clone(path), "faults", strconv.Itoa(i)), &s.Faults[i])
	}
	if v.node(append(path, "duration")...) == nil {
		return
	}
	v.positive(append(path, "duration"), s.Duration)
	for i, f := range s.Faults {
		if end := f.After + f.For; f.After > 0 && end > s.Duration {
			v.ps.add(append(path, "duration"), fmt.Sprintf("traffic stops at %s, before faults[%d] ends at %s; the fault would not be exercised", s.Duration, i, end))
		}
	}
}

// fault checks one fault against the edges and writes its canonical status
// code back. The schema has already fixed the fault's shape: one target,
// one trigger, and only its kind's fields.
func (v *validator) fault(path []string, ft *Fault) {
	f := fields(v.node(path...))
	at := func(key string) []string { return append(slices.Clone(path), key) }

	var edge Edge
	known := true
	if ft.Edge != "" {
		edge, known = v.cfg.Edge(ft.Edge)
		if !known {
			v.ps.add(at("edge"), fmt.Sprintf("%s is not a declared edge; faults go only on edges listed under edges", ft.Edge)+suggest(ft.Edge, v.edgeNames()))
		}
	}
	layer := ft.Kind.Layer()
	switch {
	case ft.Service == Traffic:
		v.ps.add(at("service"), "traffic is the load, not part of the system; faults go on the system's services and edges")
	case known && layer == Request && !edge.Protocol.RequestLevel():
		v.ps.add(at("edge"), fmt.Sprintf("%s fault needs an http or grpc edge; %s is %s, so the proxy sees bytes, not requests", article(ft.Kind), ft.Edge, edge.Protocol))
	case layer == Network && ft.Service != "" && len(v.cfg.Inbound(ft.Service)) == 0:
		v.ps.add(at("service"), fmt.Sprintf("%s fault on a service applies to its declared inbound edges, and %s has none; declare an edge into %s", article(ft.Kind), ft.Service, ft.Service))
	}

	if f["delay"] != nil {
		v.positive(at("delay"), ft.Delay)
	}
	if f["jitter"] != nil && ft.Jitter > ft.Delay {
		v.ps.add(at("jitter"), fmt.Sprintf("jitter (%s) is more than delay (%s), so the delay could go negative", ft.Jitter, ft.Delay))
	}
	if f["rate"] != nil {
		v.nonZero(at("rate"), f["rate"].Value, ft.Rate)
	}
	if f["load"] != nil {
		v.nonZero(at("load"), f["load"].Value, ft.Load)
	}
	if f["limit"] != nil && ft.Limit <= 0 {
		v.ps.add(at("limit"), "must be above zero; use down to stop traffic entirely")
	}
	if f["restart_after"] != nil {
		v.positive(at("restart_after"), ft.RestartAfter)
	}
	if f["code"] != nil && known && edge.Protocol.RequestLevel() {
		code, problem := statusCode(edge.Protocol, ft.Code)
		if problem != "" {
			v.ps.add(at("code"), problem)
		}
		ft.Code = code
	}

	if f["for"] != nil {
		if ft.Kind == Crash {
			v.ps.add(at("for"), "a crash is instant; restart_after sets how long the service stays down")
		}
		v.positive(at("for"), ft.For)
	}
	if f["after_requests"] != nil && known {
		v.requestTrigger(at("after_requests"), *ft, edge)
	}
}

// requestTrigger checks that an after_requests trigger has requests to
// count: the proxy parses requests only on http and grpc edges.
func (v *validator) requestTrigger(path []string, ft Fault, edge Edge) {
	if ft.Edge != "" {
		if !edge.Protocol.RequestLevel() {
			v.ps.add(path, fmt.Sprintf("%s is a %s edge: its proxy sees bytes, not requests, so there are none to count. Use after: <duration>, or declare the edge http or grpc if it carries those", ft.Edge, edge.Protocol))
		}
		return
	}
	for _, e := range v.cfg.Inbound(ft.Service) {
		if e.Protocol.RequestLevel() {
			return
		}
	}
	v.ps.add(path, fmt.Sprintf("after_requests on a service counts requests on its declared http and grpc inbound edges, and %s has none. Use after: <duration>", ft.Service))
}

// article prefixes a fault kind with "a" or "an".
func article(k FaultKind) string {
	if k != "" && strings.ContainsRune("aeiou", rune(k[0])) {
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

// statusCode returns the canonical code for an errors fault: an HTTP error
// status as digits, or a gRPC error code name. A non-empty second value is
// the problem. The schema bounds a written number to 400-599; a quoted
// number or a name arrives as text and is checked here.
func statusCode(p Protocol, v string) (string, string) {
	if p == HTTP {
		c, err := strconv.Atoi(v)
		if err != nil || c < 400 || c > 599 {
			return v, fmt.Sprintf("%q is not an HTTP error status; errors injects failures, so use a number from 400 to 599, such as 503", v)
		}
		return strconv.Itoa(c), ""
	}
	name := strings.ToUpper(v)
	if c, err := strconv.Atoi(v); err == nil {
		if c < 0 || c >= len(grpcCodes) {
			return v, fmt.Sprintf("%d is not a gRPC status code (0 to %d)", c, len(grpcCodes)-1)
		}
		name = grpcCodes[c]
	} else if !slices.Contains(grpcCodes, name) {
		return v, fmt.Sprintf("%q is not a gRPC status code, such as UNAVAILABLE", v) + suggest(name, grpcCodes)
	}
	if name == "OK" {
		return v, "OK is success; errors injects failures, so use a code such as UNAVAILABLE"
	}
	return name, ""
}

func (v *validator) defaults() {
	if v.cfg.Defaults.ErrorRateWithin > 100 {
		v.ps.add([]string{"defaults", "error_rate_within"}, "can't exceed 100pp")
	}
	if v.node("defaults", "recovers_within") != nil {
		v.positive([]string{"defaults", "recovers_within"}, v.cfg.Defaults.RecoversWithin)
	}
}

func (v *validator) checks() {
	names := map[string]bool{}
	for i, c := range v.cfg.Checks {
		path := []string{"checks", strconv.Itoa(i)}
		at := func(keys ...string) []string { return append(slices.Clone(path), keys...) }
		if names[c.Name] {
			v.ps.add(at("name"), fmt.Sprintf("another check is already named %q", c.Name))
		}
		names[c.Name] = true

		body := string(c.Kind)
		switch c.Kind {
		case ImageCheck:
		case SQLCheck:
			v.connection(at(body, "on"), c.SQL.On, "postgres")
		case HTTPCheck:
			v.connection(at(body, "on"), c.HTTP.On, "http")
		case RecoversCheck:
			v.positive(at(body, "within"), c.Recovers.Within)
			v.measured(at(body, "service"), c.Recovers.Service)
		case NoRegressionCheck:
			v.measured(at(body, "service"), c.NoRegression.Service)
		case SLOCheck:
			if c.SLO.Measure != ErrorRate {
				v.positive(at(body, "max"), c.SLO.MaxLatency)
			}
			v.measured(at(body, "service"), c.SLO.Service)
		case MetricCheck:
			if m := c.Metric; m.Max != nil && m.Min != nil && *m.Min > *m.Max {
				v.ps.add(at(body), fmt.Sprintf("min (%g) is above max (%g), so no value can pass", *m.Min, *m.Max))
			}
		}
	}
}

// connection checks that a check's connection exists and has a URL scheme
// the check can use: postgres for sql, http or https for http.
func (v *validator) connection(path []string, name, scheme string) {
	var names []string
	for _, c := range v.cfg.Connections {
		if c.Name != name {
			names = append(names, c.Name)
			continue
		}
		got, _, _ := strings.Cut(c.URL, "://")
		if !strings.HasPrefix(got, scheme) {
			v.ps.add(path, fmt.Sprintf("%s is a %s:// connection; this check needs a %s:// one", name, got, scheme))
		}
		return
	}
	msg := fmt.Sprintf("no connection named %q; declare it under connections", name)
	if hint := suggest(name, names); hint != "" {
		msg += hint
	} else if len(names) > 0 {
		msg += " (declared: " + strings.Join(names, ", ") + ")"
	}
	v.ps.add(path, msg)
}

// measured checks that a service has a declared inbound edge whose proxy
// measures requests, which recovers, no_regression and slo read.
func (v *validator) measured(path []string, service string) {
	in := v.cfg.Inbound(service)
	for _, e := range in {
		if e.Protocol.RequestLevel() {
			return
		}
	}
	if len(in) == 0 {
		var callees []string
		for _, e := range v.cfg.Edges {
			callees = append(callees, e.Callee)
		}
		v.ps.add(path, fmt.Sprintf("%s has no declared inbound edges, so no proxy measures it; declare an http or grpc edge into %s", service, service)+suggest(service, callees))
		return
	}
	v.ps.add(path, fmt.Sprintf("%s's declared inbound edges are all tcp, where the proxy sees bytes, not requests; this check needs an http or grpc edge into %s", service, service))
}

func (v *validator) suiteEntry(path []string, name string) {
	if _, ok := v.cfg.Scenario(name); ok {
		return
	}
	if name == "all" {
		v.ps.add(path, `"all" is written as the suite's value (pr: all), not as an entry in its list`)
		return
	}
	isAuto := name == "auto" || strings.HasPrefix(name, "auto-")
	if isAuto && !v.cfg.AutoScenarios {
		v.ps.add(path, fmt.Sprintf("%s is a generated scenario; set auto_scenarios: true to generate them", name))
		return
	}
	switch {
	case name == "auto":
		return
	case name == "auto-crash-"+Traffic:
		v.ps.add(path, "traffic is the load, not part of the system; no crash scenario is generated for it")
		return
	case strings.HasPrefix(name, "auto-crash-") && len(name) > len("auto-crash-"):
		// Services come from the system file, which the driver reads.
		return
	case strings.HasPrefix(name, "auto-down-"):
		for _, e := range v.cfg.Edges {
			if !e.UserFacing() && name == "auto-down-"+e.Caller+"-"+e.Callee {
				return
			}
		}
	}
	var names []string
	for _, s := range v.cfg.Scenarios {
		names = append(names, s.Name)
	}
	if v.cfg.AutoScenarios {
		for _, e := range v.cfg.Edges {
			if !e.UserFacing() {
				names = append(names, "auto-down-"+e.Caller+"-"+e.Callee)
			}
		}
	}
	v.ps.add(path, fmt.Sprintf("no scenario named %q", name)+suggest(name, names))
}

func (v *validator) edgeNames() []string {
	names := make([]string, len(v.cfg.Edges))
	for i, e := range v.cfg.Edges {
		names[i] = e.Name()
	}
	return names
}

func (v *validator) positive(path []string, d time.Duration) {
	if d <= 0 {
		v.ps.add(path, "must be longer than zero")
	}
}

// nonZero checks a share the schema has bounded at 100%: zero would never
// fire.
func (v *validator) nonZero(path []string, written string, p Percent) {
	if p <= 0 {
		v.ps.add(path, fmt.Sprintf("%s would never fire; use a share above 0%%", written))
	}
}
