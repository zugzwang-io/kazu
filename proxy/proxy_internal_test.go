package proxy

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// forwardOnce runs forward for one piped caller connection, dialling a port
// nothing listens on, and returns what it logged.
func forwardOnce(ctx context.Context, t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := ln.Addr().String()
	ln.Close()

	caller, peer := net.Pipe()
	defer peer.Close()
	var log strings.Builder
	New(refused, slog.New(slog.NewTextHandler(&log, nil))).forward(ctx, caller)
	return log.String()
}

func TestDialFailureIsLoggedWithItsCause(t *testing.T) {
	got := forwardOnce(context.Background(), t)
	if !strings.Contains(got, "dial upstream") || !strings.Contains(got, "connection refused") {
		t.Fatalf("log %q doesn't name the failed dial and its cause", got)
	}
}

func TestDialCutShortByShutdownIsNotLogged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := forwardOnce(ctx, t); got != "" {
		t.Fatalf("a dial cancelled by shutdown was logged: %q", got)
	}
}
