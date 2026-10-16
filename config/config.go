package config

import (
	"fmt"
	"time"
)

// Config is a parsed and validated kazu.yaml. Maps from the file become
// slices in file order, so anything derived from a config is deterministic
// (Service.Environment and ServiceEnv.Vars stay maps; sort their keys).
//
// The loader fills every documented default (HTTPRequest.Method) so a zero
// or empty value after loading is one the user wrote, never one they left out.
//
// Config mirrors kazu.schema.json, which publishes where each key's shape
// is headed, so it can hold more than the running build does; the loader
// rejects that rest as not supported yet.
type Config struct {
	System      System
	Traffic     Service
	Edges       []Edge
	Connections []Connection
	Scenarios   []Scenario
	Checks      []Check
	Suites      []Suite
}

// System is where the system under test is described: exactly one of a
// compose file or a directory of rendered Kubernetes manifests.
type System struct {
	Compose   string
	Manifests string
}

// Service is the subset of a compose service that Kazu takes for the images
// it runs (traffic and code checks), with the same meaning as in compose.
// A nil Command keeps the image's default.
type Service struct {
	Image       string
	Command     []string
	Volumes     []string
	Environment map[string]string
}

// Protocol is how Kazu's proxy treats an edge.
type Protocol string

const (
	TCP  Protocol = "tcp"
	HTTP Protocol = "http"
	GRPC Protocol = "grpc"
)

// RequestLevel reports whether the proxy parses requests on this protocol,
// which request-level faults, request-count triggers and per-request
// latency need.
func (p Protocol) RequestLevel() bool { return p == HTTP || p == GRPC }

// Traffic is the reserved name of the traffic service in edges.
const Traffic = "traffic"

// Edge is a declared caller -> callee connection, the only place faults go.
type Edge struct {
	Caller, Callee string
	Protocol       Protocol
	TLS            *TLS
}

// Name is the edge's canonical name, "caller -> callee".
func (e Edge) Name() string { return e.Caller + " -> " + e.Callee }

// UserFacing reports whether the edge starts at traffic; the default
// regression checks read these edges.
func (e Edge) UserFacing() bool { return e.Caller == Traffic }

// TLS is a test CA for terminating TLS on an edge. Both values are secret
// references (a file path or ${env:NAME}), resolved at run time.
type TLS struct {
	CACert, CAKey string
}

// Connection is a name checks use with run.sql or run.http, and the URL Kazu
// connects to.
type Connection struct {
	Name, URL string
}

// Severity is what a missing signal does to the verdict.
type Severity string

const (
	Fail Severity = "fail"
	Warn Severity = "warn"
)

// Scenario is a named set of faults, env vars and traffic overrides.
type Scenario struct {
	Name   string
	Faults []Fault
	// Env holds env vars per service, passed through untouched.
	Env []ServiceEnv
	// Traffic, when set, is merged over Config.Traffic: a non-empty Image,
	// a non-nil Command or non-nil Volumes replace, Environment is merged.
	Traffic *Service
	// Duration overrides the computed KAZU_DURATION; zero means computed.
	Duration time.Duration
	// NotExercised is the scenario's severity for a fault that never
	// landed; empty means not set.
	NotExercised Severity
	// Checks run in this scenario only, after the top-level checks.
	Checks []Check
}

// ServiceEnv is a scenario's env vars for one service.
type ServiceEnv struct {
	Service string
	Vars    map[string]string
}

// FaultKind is what a fault does.
type FaultKind string

const (
	Latency   FaultKind = "latency"
	Down      FaultKind = "down"
	Blackhole FaultKind = "blackhole"
	Reset     FaultKind = "reset"
	Bandwidth FaultKind = "bandwidth"
	Errors    FaultKind = "errors"
	Timeout   FaultKind = "timeout"
	Crash     FaultKind = "crash"
	Pause     FaultKind = "pause"
	CPU       FaultKind = "cpu"
	Memory    FaultKind = "memory"
	DiskFull  FaultKind = "disk_full"
)

// Layer is where a fault is applied.
type Layer int

const (
	// Network faults go to proxies: one edge, or every declared inbound edge
	// of a service.
	Network Layer = iota
	// Request faults need a proxy that parses HTTP or gRPC, on one edge.
	Request
	// Process faults act on a service's container: its process or its
	// resources.
	Process
)

// Layer returns where the fault kind is applied. The schema admits only the
// kinds above, so any other value is a programming error.
func (k FaultKind) Layer() Layer {
	switch k {
	case Latency, Down, Blackhole, Reset, Bandwidth:
		return Network
	case Errors, Timeout:
		return Request
	case Crash, Pause, CPU, Memory, DiskFull:
		return Process
	}
	panic(fmt.Sprintf("config: unknown fault kind %q", string(k)))
}

// Fault is one entry of a scenario's faults list. Exactly one of Edge and
// Service is set. Fields that don't apply to Kind are zero.
type Fault struct {
	// Name is the name given in the file, empty if none.
	Name string
	// Edge is a declared edge's canonical name, "caller -> callee"; the
	// loader normalises the spacing the schema allows around the arrow.
	Edge    string
	Service string
	Kind    FaultKind

	Delay, Jitter time.Duration // latency
	Rate          Percent       // reset, errors, timeout
	Code          string        // errors: an HTTP status ("503") or a gRPC code name ("UNAVAILABLE")
	Limit         BitRate       // bandwidth
	Load          Percent       // cpu, memory
	// RestartAfter is how long a crashed container stays down before Kazu
	// restarts it; zero means right away.
	RestartAfter time.Duration

	// After counts from traffic start, or from the latest of From's anchors
	// when From is set; AfterRequests counts requests through the
	// proxy. At most one is set; neither means at traffic start or anchor.
	After         time.Duration
	AfterRequests int
	From          []Anchor
	// For is how long the fault lasts; zero means until traffic ends.
	For time.Duration
}

// Anchor is a point in another fault's planned window: its end, or its
// start when Start is set.
type Anchor struct {
	Fault string
	Start bool
}

// Target is the fault's edge or service name, for messages.
func (f Fault) Target() string {
	if f.Edge != "" {
		return f.Edge
	}
	return f.Service
}

// Percent is a percentage as written: "20%" is 20.
type Percent float64

// BitRate is bits per second.
type BitRate int64

// CheckKind is the kind of a user check.
type CheckKind string

const (
	ImageCheck        CheckKind = "image"
	SQLCheck          CheckKind = "sql"
	HTTPCheck         CheckKind = "http"
	RecoversCheck     CheckKind = "recovers"
	NoRegressionCheck CheckKind = "no_regression"
	SLOCheck          CheckKind = "slo"
	MetricCheck       CheckKind = "metric"
)

// Check is one entry of the checks list. The field matching Kind is set.
type Check struct {
	// Name is the name given in the file, or "<kind>-<n>" where n is the
	// check's position, from 1, in the list its scenario runs: the top-level
	// checks, then the scenario's own.
	Name string
	Kind CheckKind

	Image        *Service
	SQL          *SQL
	HTTP         *HTTPRequest
	Recovers     *Recovers
	NoRegression *NoRegression
	SLO          *SLO
	Metric       *Metric
}

// SQL runs Query on a connection after traffic exits and passes when it
// returns no rows: the query selects violations.
type SQL struct {
	On, Query string
}

// HTTPRequest sends one request to a connection after traffic exits. Method
// is GET when the file doesn't say.
type HTTPRequest struct {
	On, Method, Path string
	// ExpectStatus is zero when not checked; ExpectBody is a substring of
	// the response body, empty when not checked.
	ExpectStatus int
	ExpectBody   string
}

// Recovers checks that a service's error rate returns to its pre-fault level.
type Recovers struct {
	Service string
	Within  time.Duration
}

// Measure is a proxy measurement on a service's declared inbound edges.
type Measure string

const (
	P50       Measure = "p50"
	P90       Measure = "p90"
	P95       Measure = "p95"
	P99       Measure = "p99"
	ErrorRate Measure = "error_rate"
)

// NoRegression compares head with base: head may be worse by at most Within.
type NoRegression struct {
	Service string
	Measure Measure
	Within  Percent
}

// SLO is an absolute bound: MaxLatency for latency measures, MaxRate for
// error_rate.
type SLO struct {
	Service    string
	Measure    Measure
	MaxLatency time.Duration
	MaxRate    Percent
}

// Metric is a PromQL query with absolute bounds (Max, Min) or a bound
// relative to base (Within).
type Metric struct {
	Query    string
	Max, Min *float64
	Within   *Percent
	// NoData is the check's severity when the query has no samples; empty
	// means not set.
	NoData Severity
}

// Suite is a named set of scenarios. All means every scenario. Otherwise
// Scenarios lists names as written, where "auto" stands for every auto
// scenario (not supported yet).
type Suite struct {
	Name      string
	All       bool
	Scenarios []string
}

// Edge returns the declared edge with the given canonical name.
func (c *Config) Edge(name string) (Edge, bool) {
	for _, e := range c.Edges {
		if e.Name() == name {
			return e, true
		}
	}
	return Edge{}, false
}

// Inbound returns the declared edges into service, in file order.
func (c *Config) Inbound(service string) []Edge {
	var in []Edge
	for _, e := range c.Edges {
		if e.Callee == service {
			in = append(in, e)
		}
	}
	return in
}

// Scenario returns the scenario with the given name.
func (c *Config) Scenario(name string) (Scenario, bool) {
	for _, s := range c.Scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}
