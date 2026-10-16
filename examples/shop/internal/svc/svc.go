// Package svc holds what every shop service shares: reading the env vars
// that configure it, its HTTP plumbing, and the context for writes that must
// outlive their caller.
package svc

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Env returns the env var name, or def when it is unset or empty.
func Env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// EnvInt is Env for the services' counts (pool size, in-flight limit,
// attempts, workers), all of which must be at least 1; any other value stops
// the service rather than hanging or shedding everything.
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := positiveInt(v)
	if err != nil {
		log.Fatalf("%s=%q: %v", name, v, err)
	}
	return n
}

func positiveInt(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New("not an integer")
	}
	if n < 1 {
		return 0, errors.New("must be at least 1")
	}
	return n, nil
}

// EnvDuration is Env for durations such as "500ms".
func EnvDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Fatalf("%s=%q: not a duration", name, v)
	}
	return d
}

// Healthz answers GET /healthz: 200 when ready reports no error (or is nil),
// 503 with the error otherwise.
func Healthz(ready func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ready != nil {
			if err := ready(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// DetachTimeout bounds a write that must outlive its caller. It is longer
// than the services' statement timeout, so the database gives up first when
// it is answering at all, but a database that stops answering can't hold the
// write, and the request slot behind it, until TCP gives up minutes later.
const DetachTimeout = 5 * time.Second

// Detach returns a context that ignores the caller's cancellation, for
// writes that must happen once money has moved, but still ends after
// DetachTimeout.
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), DetachTimeout)
}
