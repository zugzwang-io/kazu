// Command fakestripe serves the card-processor stand-in over HTTPS.
package main

import (
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
)

func main() {
	svc.Main("fakestripe")
	s := &fakestripe.Server{
		Latency: svc.EnvDuration("LATENCY", 200*time.Millisecond),
		Jitter:  svc.EnvDuration("JITTER", 50*time.Millisecond),
	}
	svc.Serve(svc.Env("ADDR", ":8443"), s.Handler(),
		svc.Env("TLS_CERT", "/certs/fakestripe.pem"), svc.Env("TLS_KEY", "/certs/fakestripe-key.pem"))
}
