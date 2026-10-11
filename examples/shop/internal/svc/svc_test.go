package svc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()

	if err := Healthcheck(srv.URL); err != nil {
		t.Fatalf("200: %v", err)
	}
	code = http.StatusServiceUnavailable
	if err := Healthcheck(srv.URL); err == nil {
		t.Fatal("503 passed")
	}
	srv.Close()
	if err := Healthcheck(srv.URL); err == nil {
		t.Fatal("a closed server passed")
	}
}
