package config

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"
)

// overlaps rejects two faults of the same kind on one edge unless they
// provably can't be active at once, whatever their directions. A fault on a
// service counts as one on each of its declared inbound edges. Two faults
// can't overlap when their windows count from the same point and don't
// intersect, or when one is sequenced after the other's end through from.
// It needs anchors that resolve: every from names a fault, with no loops.
func (v *validator) overlaps(path []string, faults []Fault, byName map[string]int) {
	ws := windows(faults, byName)
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
			edge, shared := sharedEdge(v.faultEdges(a), v.faultEdges(b))
			if !shared || ws[i].disjoint(ws[j]) || sequenced(faults, byName, j, i) || sequenced(faults, byName, i, j) {
				continue
			}
			v.ps.add(append(slices.Clone(path), "faults", strconv.Itoa(j)), fmt.Sprintf(
				"%s and %s are both %s faults on %s and can be active at once; keep one, or sequence them with from: so one starts after the other ends",
				label(i), label(j), a.Kind, edge))
		}
	}
}

// faultEdges returns the edges a fault acts on: its edge, or for a network
// fault on a service, the service's declared inbound edges.
func (v *validator) faultEdges(f Fault) []string {
	if f.Edge != "" {
		return []string{f.Edge}
	}
	if f.Kind.Layer() != Network {
		return nil
	}
	var names []string
	for _, e := range v.cfg.Inbound(f.Service) {
		names = append(names, e.Name())
	}
	return names
}

func sharedEdge(a, b []string) (string, bool) {
	for _, e := range a {
		if slices.Contains(b, e) {
			return e, true
		}
	}
	return "", false
}

// window is a fault's half-open interval [start, end), counted from origin:
// traffic start, or the trigger of the request-count fault at that index.
type window struct {
	origin     int
	start, end time.Duration
}

const (
	fromTraffic = -1
	forever     = time.Duration(math.MaxInt64)
)

// disjoint reports whether two windows are known not to intersect. A nil
// window isn't known before the run.
func (w *window) disjoint(o *window) bool {
	return w != nil && o != nil && w.origin == o.origin && (w.end <= o.start || o.end <= w.start)
}

// windows resolves every fault's window that can be known before the run.
// A window is nil when its from joins anchors counted from different points.
func windows(faults []Fault, byName map[string]int) []*window {
	ws := make([]*window, len(faults))
	seen := make([]bool, len(faults))
	var resolve func(i int) *window
	resolve = func(i int) *window {
		if seen[i] {
			return ws[i]
		}
		seen[i] = true
		f := faults[i]
		w := &window{origin: fromTraffic}
		if f.AfterRequests > 0 {
			w.origin = i
		}
		for n, a := range f.From {
			aw := resolve(byName[a.Fault])
			if aw == nil || n > 0 && aw.origin != w.origin {
				return nil
			}
			t := aw.end
			if a.Start {
				t = aw.start
			}
			if n == 0 || t > w.start {
				w.origin, w.start = aw.origin, t
			}
		}
		w.start += f.After
		w.end = forever
		if f.For > 0 {
			w.end = w.start + f.For
		}
		ws[i] = w
		return w
	}
	for i := range faults {
		resolve(i)
	}
	return ws
}

// sequenced reports whether fault j starts no earlier than fault i ends
// through from: one of j's anchors is i's end, or the start or end of a
// fault itself sequenced after i's end.
func sequenced(faults []Fault, byName map[string]int, j, i int) bool {
	for _, a := range faults[j].From {
		k := byName[a.Fault]
		if !a.Start && k == i || sequenced(faults, byName, k, i) {
			return true
		}
	}
	return false
}
