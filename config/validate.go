package config

import (
	"fmt"
	"maps"
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
	top := v.checks([]string{"checks"}, cfg.Checks, nil)
	for _, s := range cfg.Scenarios {
		v.scenario(s, top)
	}
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

// scenario checks one scenario. taken holds the top-level check names,
// which run in every scenario alongside its own.
func (v *validator) scenario(s Scenario, taken map[string]bool) {
	path := []string{"scenarios", s.Name}
	if s.Name == "auto" || s.Name == "all" || strings.HasPrefix(s.Name, "auto-") {
		v.ps.addKey(path, fmt.Sprintf("%q is reserved: \"all\" and \"auto\" are suite keywords, and names starting with \"auto-\" belong to generated scenarios", s.Name))
	}
	for i := range s.Faults {
		v.fault(append(slices.Clone(path), "faults", strconv.Itoa(i)), &s.Faults[i])
	}
	v.anchors(path, s.Faults)
	v.checks(append(slices.Clone(path), "checks"), s.Checks, taken)
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
		v.ps.add(at("edge"), fmt.Sprintf("%s faults need an http or grpc edge; %s is %s, so the proxy sees bytes, not requests", ft.Kind, ft.Edge, edge.Protocol))
	case layer == Network && ft.Service != "" && len(v.cfg.Inbound(ft.Service)) == 0:
		v.ps.add(at("service"), fmt.Sprintf("%s faults on a service apply to its declared inbound edges, and %s has none; declare an edge into %s", ft.Kind, ft.Service, ft.Service))
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

// anchors checks what each fault's from names: a fault in the same
// scenario, not itself, with an end if its end is used, and no loops.
func (v *validator) anchors(path []string, faults []Fault) {
	at := func(i int, keys ...string) []string {
		return append(append(slices.Clone(path), "faults", strconv.Itoa(i)), keys...)
	}
	byName := map[string]int{}
	var names []string
	for i, f := range faults {
		if f.Name == "" {
			continue
		}
		if _, dup := byName[f.Name]; dup {
			v.ps.add(at(i, "name"), fmt.Sprintf("another fault in this scenario is already named %q", f.Name))
			continue
		}
		byName[f.Name] = i
		names = append(names, f.Name)
	}
	next := make([][]int, len(faults))
	for i, f := range faults {
		from := at(i, "from")
		n := v.node(from...)
		listed := n != nil && n.Kind == yaml.SequenceNode
		for j, a := range f.From {
			apath := from
			if listed {
				apath = append(slices.Clone(from), strconv.Itoa(j))
			}
			k, ok := byName[a.Fault]
			switch {
			case !ok:
				v.ps.add(apath, fmt.Sprintf("no fault named %q in this scenario", a.Fault)+suggest(a.Fault, names))
			case k == i:
				v.ps.add(apath, "a fault can't count from itself")
			case a.Start:
				next[i] = append(next[i], k)
			case faults[k].Kind == Crash:
				v.ps.add(apath, fmt.Sprintf("a crash is instant, so %s has no end; count from { start: %s }", a.Fault, a.Fault))
			case v.node(at(k, "for")...) == nil:
				v.ps.add(apath, fmt.Sprintf("%s has no for, so it lasts until traffic ends; give it a for, or count from { start: %s }", a.Fault, a.Fault))
			default:
				next[i] = append(next[i], k)
			}
		}
	}
	for _, loop := range loops(next) {
		var chain []string
		for _, i := range loop {
			chain = append(chain, faults[i].Name)
		}
		chain = append(chain, chain[0])
		v.ps.add(at(loop[0], "from"), "faults count from each other in a loop: "+strings.Join(chain, " -> "))
	}
}

// loops returns the cycles a depth-first walk of next finds (at least one
// whenever next has a cycle), each starting at its lowest-numbered node, in
// order of that node.
func loops(next [][]int) [][]int {
	const (
		unseen = iota
		open
		done
	)
	state := make([]int, len(next))
	var stack []int
	var found [][]int
	var visit func(i int)
	visit = func(i int) {
		state[i] = open
		stack = append(stack, i)
		for _, k := range next[i] {
			switch state[k] {
			case unseen:
				visit(k)
			case open:
				loop := stack[slices.Index(stack, k):]
				lo := slices.Index(loop, slices.Min(loop))
				found = append(found, slices.Concat(loop[lo:], loop[:lo]))
			}
		}
		stack = stack[:len(stack)-1]
		state[i] = done
	}
	for i := range next {
		if state[i] == unseen {
			visit(i)
		}
	}
	slices.SortFunc(found, func(a, b []int) int { return a[0] - b[0] })
	return found
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

// checks checks one checks list. taken holds the names of checks that run
// alongside it; the result adds this list's.
func (v *validator) checks(list []string, cs []Check, taken map[string]bool) map[string]bool {
	names := maps.Clone(taken)
	if names == nil {
		names = map[string]bool{}
	}
	for i, c := range cs {
		path := append(slices.Clone(list), strconv.Itoa(i))
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
	return names
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
	var names []string
	for _, s := range v.cfg.Scenarios {
		names = append(names, s.Name)
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
