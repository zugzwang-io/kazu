package fakestripe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdempotentWithRealisticLatency(t *testing.T) {
	s := &Server{Latency: 20 * time.Millisecond, Jitter: 5 * time.Millisecond}
	ids := map[string]bool{}
	for _, key := range []string{"a", "a", "b"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/charges", nil)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		start := time.Now()
		s.Handler().ServeHTTP(rec, req)
		if took := time.Since(start); took < 15*time.Millisecond {
			t.Fatalf("responded in %v, want at least 15ms", took)
		}
		var out struct{ ID string }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
			t.Fatalf("bad response %d %s", rec.Code, rec.Body)
		}
		ids[out.ID] = true
	}
	if len(ids) != 2 || s.Count() != 2 {
		t.Fatalf("%d distinct ids, %d charges; want 2 and 2", len(ids), s.Count())
	}
}
