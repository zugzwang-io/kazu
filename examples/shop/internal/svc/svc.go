// Package svc holds the plumbing every shop service shares: env-var config,
// serving with graceful shutdown, and the healthcheck command.
package svc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Main sets up logging for the named service. If the binary was run as
// `<binary> healthcheck <url>`, it runs the healthcheck and exits instead:
// the images have no shell or curl, so compose healthchecks call the binary.
func Main(name string) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", name))
	if len(os.Args) == 3 && os.Args[1] == "healthcheck" {
		if err := Healthcheck(os.Args[2]); err != nil {
			Fatal("healthcheck", "err", err)
		}
		os.Exit(0)
	}
}

// Env returns the env var name, or def when it is unset or empty.
func Env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// EnvInt is Env for integers; a malformed value is a startup error.
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		Fatal("bad integer env var", "name", name, "value", v)
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
		Fatal("bad duration env var", "name", name, "value", v)
	}
	return d
}

func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// Serve runs h on addr until SIGTERM or SIGINT, then drains for up to 5 s.
func Serve(addr string, h http.Handler) {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	select {
	case err := <-errc:
		Fatal("server stopped", "err", err)
	case <-stop:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// HTTPClient is the client services use to call each other: the default
// transport, keeping enough idle connections for the shop's concurrency.
func HTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 64
	return &http.Client{Transport: t}
}

// Healthcheck GETs url and succeeds on a 2xx.
func Healthcheck(url string) error {
	c := HTTPClient()
	c.Timeout = 2 * time.Second
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New(resp.Status)
	}
	return nil
}
