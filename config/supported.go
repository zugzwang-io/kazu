package config

import (
	"fmt"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// unsupported reports what the published contract allows but this build of
// Kazu doesn't do yet, so a config never asks for something that silently
// does nothing. Each feature deletes its entry here when it's built; the
// schema doesn't change.
func (v *validator) unsupported() {
	if v.cfg.System.Manifests != "" {
		v.ps.addKey([]string{"system", "manifests"}, "Kubernetes manifests are not supported yet; use a compose file")
	}
	for i, kv := range pairs(v.node("edges")) {
		p := v.cfg.Edges[i].Protocol
		if p == TCP {
			continue
		}
		path := []string{"edges", kv.k.Value}
		if kv.v.Kind == yaml.MappingNode {
			path = append(path, "protocol")
		}
		v.ps.add(path, fmt.Sprintf("%s edges are not supported yet; declare it tcp, where latency and down work", p))
	}
	v.supportedChecks([]string{"checks"}, v.cfg.Checks)
	for _, s := range v.cfg.Scenarios {
		path := []string{"scenarios", s.Name}
		at := func(keys ...string) []string { return append(slices.Clone(path), keys...) }
		if s.Traffic != nil {
			v.ps.addKey(at("traffic"), "a scenario's traffic is not supported yet; vary traffic with env")
		}
		if s.NotExercised != "" {
			v.ps.addKey(at("not_exercised"), "not_exercised is not supported yet; a fault that never lands always makes the scenario not exercised")
		}
		for i, f := range s.Faults {
			v.supportedFault(at("faults", strconv.Itoa(i)), f)
		}
		v.supportedChecks(at("checks"), s.Checks)
	}
	for _, s := range v.cfg.Suites {
		for i, name := range s.Scenarios {
			if generated(name) {
				v.ps.add([]string{"suites", s.Name, strconv.Itoa(i)}, "generated scenarios are not supported yet")
			}
		}
	}
}

func (v *validator) supportedFault(path []string, f Fault) {
	at := func(key string) []string { return append(slices.Clone(path), key) }
	if f.Kind != Latency && f.Kind != Down {
		v.ps.add(at("fault"), fmt.Sprintf("%s faults are not supported yet; latency and down are", f.Kind))
	}
	if f.Service != "" {
		v.ps.addKey(at("service"), "faults on a service are not supported yet; put the fault on an edge")
	}
	if v.node(at("after_requests")...) != nil {
		v.ps.addKey(at("after_requests"), "after_requests is not supported yet; use after")
	}
	if f.From != nil {
		v.ps.addKey(at("from"), "from is not supported yet; after counts from traffic start")
	}
}

func (v *validator) supportedChecks(path []string, cs []Check) {
	for i, c := range cs {
		if c.Kind != ImageCheck && c.Kind != SQLCheck {
			v.ps.addKey(append(slices.Clone(path), strconv.Itoa(i), string(c.Kind)), fmt.Sprintf("%s checks are not supported yet; image and sql are", c.Kind))
		}
	}
}
