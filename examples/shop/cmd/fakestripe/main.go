// Command fakestripe serves the card-processor stand-in.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
)

func main() {
	s := &fakestripe.Server{
		Latency: svc.EnvDuration("LATENCY", 200*time.Millisecond),
		Jitter:  svc.EnvDuration("JITTER", 50*time.Millisecond),
	}
	log.Fatal(http.ListenAndServe(svc.Env("ADDR", ":12111"), s.Handler()))
}
