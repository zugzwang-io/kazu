package config

import (
	"testing"
	"time"
)

// A from naming no fault, or a loop, resolves to nothing rather than to a
// wrong index, so the schedule is safe on anchors that don't resolve.
func TestScheduleUnresolvedAnchors(t *testing.T) {
	faults := []Fault{
		{Name: "a", For: 1},
		{From: []Anchor{{Fault: "missing"}}},
		{Name: "x", From: []Anchor{{Fault: "y", Start: true}}},
		{Name: "y", From: []Anchor{{Fault: "x", Start: true}}},
	}
	s := schedule{faults: faults, byName: map[string]int{"a": 0, "x": 2, "y": 3}, spans: []time.Duration{1, forever, forever, forever}, after: map[[2]int]bool{}}
	if ws := s.windows(); ws[1] != nil {
		t.Errorf("window from a missing anchor = %+v, want nil", ws[1])
	}
	if s.sequenced(1, 0) {
		t.Error("a missing anchor sequenced a fault after fault 0")
	}
	if s.sequenced(2, 0) || s.sequenced(3, 0) {
		t.Error("a loop sequenced a fault after fault 0")
	}
}
