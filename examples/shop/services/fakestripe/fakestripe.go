// Package fakestripe stands in for an external card processor inside the
// sealed system, the way stripe-mock would: POST /v1/charges, idempotent on
// the Idempotency-Key header, in memory, with a realistic response time.
package fakestripe

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Server struct {
	// Latency is the mean response time; each key gets a fixed offset within
	// ±Jitter, derived from the key so runs are repeatable.
	Latency, Jitter time.Duration

	mu      sync.Mutex
	charges map[string]string // idempotency key → charge id
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/charges", s.charge)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {})
	return mux
}

// Count returns how many distinct charges have been made.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.charges)
}

func (s *Server) charge(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		http.Error(w, `{"error":"Idempotency-Key required"}`, http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(key))
	id := "ch_" + hex.EncodeToString(sum[:8])

	// Like a real processor, the charge happens when the request arrives,
	// whether or not the caller waits for the answer.
	s.mu.Lock()
	if s.charges == nil {
		s.charges = map[string]string{}
	}
	s.charges[key] = id
	s.mu.Unlock()

	delay := s.Latency
	if s.Jitter > 0 {
		span := 2*int64(s.Jitter) + 1
		delay += time.Duration(int64(binary.BigEndian.Uint64(sum[8:16])%uint64(span)) - int64(s.Jitter))
	}
	time.Sleep(delay)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "succeeded"})
}
