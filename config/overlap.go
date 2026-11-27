package config

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
)

// overlaps rejects two faults of the same kind on one target unless they
// provably can't be active at once, whatever their directions. The target
// is the edge for network and request faults, where a service stands for
// each of its declared inbound edges, and the service for process faults.
// Two faults can't overlap when both windows are planned times that don't
// intersect, or when one is sequenced after the other's end through from.
// A fault that waits on a request count, itself or through from, has no
// planned window. Faults on undeclared targets or with a non-positive for
// are skipped: they are already reported.
func (v *validator) overlaps(path []string, faults []Fault, byName map[string]int) {
	at := func(i int, keys ...string) []string {
		return append(append(slices.Clone(path), "faults", strconv.Itoa(i)), keys...)
	}
	spans := make([]time.Duration, len(faults))
	targets := make([][]string, len(faults))
	for i, f := range faults {
		spans[i] = forever
		if v.node(at(i, "for")...) != nil {
			spans[i] = f.For
		}
		if spans[i] > 0 {
			targets[i] = v.targets(f)
		}
	}
	s := schedule{faults: faults, byName: byName, spans: spans, after: map[[2]int]bool{}}
	ws := s.windows()
	label := func(i int) string {
		if faults[i].Name != "" {
			return faults[i].Name
		}
		return "faults[" + strconv.Itoa(i) + "]"
	}
	for j, b := range faults {
		for i, a := range faults[:j] {
			if a.Kind != b.Kind {
				continue
			}
			target, shared := sharedTarget(targets[i], targets[j])
			if !shared || ws[i].disjoint(ws[j]) || s.sequenced(j, i) || s.sequenced(i, j) {
				continue
			}
			fix := "sequence them with from: so one starts after the other ends"
			if spans[i] == forever {
				fix = fmt.Sprintf("give %s a for and %s", label(i), fix)
			}
			v.ps.add(at(j), fmt.Sprintf("%s and %s are both %s faults on %s and can be active at once; keep one, or %s",
				label(i), label(j), a.Kind, target, fix))
		}
	}
}

// targets returns what a fault acts on: its declared edge, a service's
// declared inbound edges for a network fault, or the service for a process
// fault.
func (v *validator) targets(f Fault) []string {
	switch {
	case f.Edge != "":
		if _, ok := v.cfg.Edge(f.Edge); ok {
			return []string{f.Edge}
		}
		return nil
	case f.Service == Traffic:
		return nil
	case f.Kind.Layer() == Process:
		return []string{f.Service}
	}
	var names []string
	for _, e := range v.cfg.Inbound(f.Service) {
		names = append(names, e.Name())
	}
	return names
}

func sharedTarget(a, b []string) (string, bool) {
	for _, t := range a {
		if slices.Contains(b, t) {
			return t, true
		}
	}
	return "", false
}

// window is a fault's planned half-open interval [start, end) from traffic
// start.
type window struct{ start, end time.Duration }

const forever = time.Duration(math.MaxInt64)

// disjoint reports whether two windows are known not to intersect. A nil
// window isn't known before the run.
func (w *window) disjoint(o *window) bool {
	return w != nil && o != nil && (w.end <= o.start || o.end <= w.start)
}

// later adds two non-negative durations, saturating at forever.
func later(t, d time.Duration) time.Duration {
	if t > forever-d {
		return forever
	}
	return t + d
}

// schedule answers timing questions about one scenario's faults. spans
// holds each fault's for, forever when it has none; after memoises
// sequenced.
type schedule struct {
	faults []Fault
	byName map[string]int
	spans  []time.Duration
	after  map[[2]int]bool
}

// windows resolves every fault's planned window. A window is nil when the
// fault waits on a request count, itself or through from, when from names
// no fault, or when it starts too late to hold.
func (s *schedule) windows() []*window {
	ws := make([]*window, len(s.faults))
	seen := make([]bool, len(s.faults))
	var resolve func(i int) *window
	resolve = func(i int) *window {
		if seen[i] {
			return ws[i]
		}
		seen[i] = true
		f := s.faults[i]
		if f.AfterRequests > 0 {
			return nil
		}
		var start time.Duration
		for _, a := range f.From {
			k, ok := s.byName[a.Fault]
			if !ok {
				return nil
			}
			aw := resolve(k)
			if aw == nil {
				return nil
			}
			t := aw.end
			if a.Start {
				t = aw.start
			}
			start = max(start, t)
		}
		start = later(start, f.After)
		if start == forever {
			return nil
		}
		ws[i] = &window{start, later(start, s.spans[i])}
		return ws[i]
	}
	for i := range s.faults {
		resolve(i)
	}
	return ws
}

// sequenced reports whether fault j starts no earlier than fault i ends
// through from: one of j's anchors is i's end, or the start or end of a
// fault itself sequenced after i's end. A loop sequences nothing.
func (s *schedule) sequenced(j, i int) bool {
	key := [2]int{j, i}
	if done, ok := s.after[key]; ok {
		return done
	}
	s.after[key] = false
	for _, a := range s.faults[j].From {
		k, ok := s.byName[a.Fault]
		if ok && ((!a.Start && k == i) || s.sequenced(k, i)) {
			s.after[key] = true
			return true
		}
	}
	return false
}
