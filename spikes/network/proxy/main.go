// Command proxy is a TCP forwarder that counts connections and bytes. It
// listens on LISTEN, forwards every connection to UPSTREAM, and serves its
// counters as JSON at http://COUNTERS/counters.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

type counters struct {
	connections, bytesUp, bytesDown atomic.Int64
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen, upstream, ctl := env("LISTEN", ":8081"), env("UPSTREAM", "payments:8081"), env("COUNTERS", ":9000")
	var c counters

	http.HandleFunc("/counters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{
			"connections": c.connections.Load(),
			"bytes_up":    c.bytesUp.Load(),
			"bytes_down":  c.bytesDown.Load(),
		})
	})
	go func() { log.Fatal(http.ListenAndServe(ctl, nil)) }() //nolint:gosec // spike: no timeouts needed

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("proxy %s -> %s, counters on %s", listen, upstream, ctl)
	for {
		down, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		c.connections.Add(1)
		go forward(down, upstream, &c)
	}
}

func forward(down net.Conn, upstream string, c *counters) {
	defer down.Close()
	up, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", upstream)
	if err != nil {
		log.Printf("dial %s: %v", upstream, err)
		return
	}
	defer up.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(counting{up, &c.bytesUp}, down); closeWrite(up) }()
	go func() { defer wg.Done(); _, _ = io.Copy(counting{down, &c.bytesDown}, up); closeWrite(down) }()
	wg.Wait()
}

// counting adds every write to n as it happens, so long-lived keep-alive
// connections show up in the counters before they close.
type counting struct {
	io.Writer
	n *atomic.Int64
}

func (w counting) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.n.Add(int64(n))
	return n, err
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
}
