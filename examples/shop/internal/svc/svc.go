// Package svc holds the plumbing every shop service shares: env-var config,
// serving with graceful shutdown, and the healthcheck command.
package svc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
// If certFile is set it serves TLS.
func Serve(addr string, h http.Handler, certFile, keyFile string) {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() {
		if certFile != "" {
			errc <- srv.ListenAndServeTLS(certFile, keyFile)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	slog.Info("listening", "addr", addr, "tls", certFile != "")

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

// HTTPClient returns a client that trusts only the CA in caFile: the sealed
// system has no public endpoints. An empty caFile gives a plain client.
func HTTPClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return &http.Client{}, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("no certificates in " + caFile)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	t.MaxIdleConnsPerHost = 64
	return &http.Client{Transport: t}, nil
}

// Healthcheck GETs url and succeeds on a 2xx. CA_FILE is honoured for HTTPS.
func Healthcheck(url string) error {
	c, err := HTTPClient(os.Getenv("CA_FILE"))
	if err != nil {
		return err
	}
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
