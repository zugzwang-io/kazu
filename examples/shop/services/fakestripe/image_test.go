//go:build image

package fakestripe_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/imagetest"
)

// The built image serves charges with its configured latency.
func TestImage(t *testing.T) {
	c := imagetest.Start(t, "fakestripe", "12111", "LATENCY=100ms", "JITTER=0s")

	charge := func(key string) (string, time.Duration) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, c.URL+"/v1/charges", nil)
		req.Header.Set("Idempotency-Key", key)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct{ ID string }
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || out.ID == "" {
			t.Fatalf("POST /v1/charges: %s", resp.Status)
		}
		return out.ID, time.Since(start)
	}

	a, took := charge("k")
	if took < 100*time.Millisecond {
		t.Errorf("answered in %v; LATENCY=100ms not applied", took)
	}
	if b, _ := charge("k"); b != a {
		t.Errorf("same key gave %q then %q", a, b)
	}
}
