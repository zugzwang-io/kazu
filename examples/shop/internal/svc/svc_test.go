package svc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPositiveInt(t *testing.T) {
	for v, want := range map[string]int{"1": 1, "20": 20} {
		if got, err := positiveInt(v); err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", v, got, err, want)
		}
	}
	for _, v := range []string{"0", "-3", "ten", "1.5"} {
		if _, err := positiveInt(v); err == nil {
			t.Errorf("%q: accepted; a zero pool, limit, attempt count or worker count hangs or breaks the service", v)
		}
	}
}

func TestHealthz(t *testing.T) {
	cases := []struct {
		name  string
		ready func(context.Context) error
		want  int
	}{
		{"no readiness check", nil, http.StatusOK},
		{"ready", func(context.Context) error { return nil }, http.StatusOK},
		{"not ready", func(context.Context) error { return errors.New("db down") }, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		Healthz(c.ready)(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
}
