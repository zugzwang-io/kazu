// Package svc holds what every shop service shares: reading its env vars,
// and the context for writes that must outlive their caller.
package svc

import (
	"context"
	"log"
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

// EnvInt is Env for integers; a malformed value stops the service.
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("%s=%q: not an integer", name, v)
	}
	return n
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
