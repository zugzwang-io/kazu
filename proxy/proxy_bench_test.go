package proxy_test

import (
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The proxy sits on every edge of the system under test, so it must never be
// what limits throughput, latency or connection rate. Each benchmark runs the
// same workload against an echo server twice, path=direct and path=proxied:
// the gap between the two is the proxy's cost, and machine noise that hits
// both mostly cancels out.
//
// bothPaths starts handle as the upstream and a Proxy in front of it, then
// runs bench once against each.
func bothPaths(b *testing.B, handle func(net.Conn), bench func(b *testing.B, addr string)) {
	upstream := serve(b, handle)
	_, proxied := startProxy(b, upstream)
	b.Run("path=direct", func(b *testing.B) { bench(b, upstream) })
	b.Run("path=proxied", func(b *testing.B) { bench(b, proxied) })
}

func benchDial(b *testing.B, addr string) net.Conn {
	b.Helper()
	c, err := (&net.Dialer{}).DialContext(b.Context(), "tcp", addr)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

// BenchmarkThroughput streams 1 MiB per op through one connection and reads
// it back, so the reported MB/s is the bulk transfer rate a caller sees.
func BenchmarkThroughput(b *testing.B) {
	bothPaths(b, echo, func(b *testing.B, addr string) {
		c := benchDial(b, addr)
		defer c.Close()
		out, in := make([]byte, 1<<20), make([]byte, 1<<20)
		b.SetBytes(int64(len(out)))
		for b.Loop() {
			// Written concurrently with the read, or both sides' buffers fill.
			errc := make(chan error, 1)
			go func() {
				_, err := c.Write(out)
				errc <- err
			}()
			if _, err := io.ReadFull(c, in); err != nil {
				b.Fatal(err)
			}
			if err := <-errc; err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRoundTrip sends small requests over persistent connections, one
// in flight per connection, and reports the p50 and p99 of each round trip.
// conns=64 shows whether the proxy holds up under concurrency, not just in
// a single exchange.
func BenchmarkRoundTrip(b *testing.B) {
	for _, conns := range []int{1, 64} {
		b.Run("conns="+strconv.Itoa(conns), func(b *testing.B) {
			bothPaths(b, echo, func(b *testing.B, addr string) {
				benchRoundTrip(b, addr, conns)
			})
		})
	}
}

// benchRoundTrip hands each op to whichever of conns workers is free; each
// worker owns one connection and times its own round trips.
func benchRoundTrip(b *testing.B, addr string, conns int) {
	ops := make(chan struct{})
	latencies := make([][]time.Duration, conns)
	var wg sync.WaitGroup
	for i := range conns {
		c := benchDial(b, addr)
		defer c.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pingPong(c, ops, &latencies[i]); err != nil {
				b.Error(err)
				for range ops { // keep b.Loop from blocking on a dead worker
				}
			}
		}()
	}
	for b.Loop() {
		ops <- struct{}{}
	}
	close(ops)
	wg.Wait()

	all := slices.Sorted(slices.Values(slices.Concat(latencies...)))
	if len(all) == 0 {
		return
	}
	b.ReportMetric(float64(all[len(all)*50/100].Nanoseconds()), "p50-ns")
	b.ReportMetric(float64(all[len(all)*99/100].Nanoseconds()), "p99-ns")
}

// pingPong does one 64-byte round trip on c per op received, appending each
// one's duration to latencies.
func pingPong(c net.Conn, ops <-chan struct{}, latencies *[]time.Duration) error {
	msg, got := make([]byte, 64), make([]byte, 64)
	for range ops {
		start := time.Now()
		if _, err := c.Write(msg); err != nil {
			return err
		}
		if _, err := io.ReadFull(c, got); err != nil {
			return err
		}
		*latencies = append(*latencies, time.Since(start))
	}
	return nil
}

// BenchmarkConnect opens a connection per op, exchanges one byte and waits
// for the server to close, measuring the per-connection cost of the proxy's
// accept and upstream dial. The server closes first so that TIME_WAIT lands
// on its listening port instead of using up the dialler's ephemeral ports.
func BenchmarkConnect(b *testing.B) {
	oneByte := func(c net.Conn) {
		buf := make([]byte, 1)
		if _, err := io.ReadFull(c, buf); err == nil {
			_, _ = c.Write(buf)
		}
	}
	bothPaths(b, oneByte, func(b *testing.B, addr string) {
		for b.Loop() {
			c := benchDial(b, addr)
			if _, err := c.Write([]byte{1}); err != nil {
				b.Fatal(err)
			}
			if got, err := io.ReadAll(c); err != nil || len(got) != 1 {
				b.Fatalf("read %d bytes, %v; want 1 byte then EOF", len(got), err)
			}
			c.Close()
		}
	})
}
