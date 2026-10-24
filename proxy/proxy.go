// Package proxy is the forwarder Kazu puts on every declared edge. A Proxy
// accepts the caller's connections, opens one upstream connection for each,
// and copies bytes both ways, passing half-closes through so request/response
// protocols that signal the end of a request with EOF keep working.
//
// Counters update as bytes arrive, not when a connection closes: a caller that
// keeps one pooled keep-alive connection for a whole trial would otherwise
// show no traffic at all until the trial ends.
package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

// Counters is a snapshot of a Proxy's traffic since it started.
type Counters struct {
	// Connections is the number of caller connections accepted, including
	// those whose upstream dial failed.
	Connections int64
	// BytesUp is bytes received from callers, BytesDown bytes received from
	// the upstream.
	BytesUp, BytesDown int64
	// DialsRefused, DialsTimedOut and DialsFailed count accepted connections
	// cut because the upstream dial failed: refused (the host is up but
	// nothing listens on the port, such as a crashed process in a running
	// container), timed out (no answer at all), or failed any other way. A
	// stopped compose service lands in DialsFailed: its name stops
	// resolving. The caller can't tell these apart; it only sees its
	// connection close. Dials cut short by shutdown aren't counted.
	DialsRefused, DialsTimedOut, DialsFailed int64
}

// Proxy forwards TCP connections to one upstream address.
type Proxy struct {
	upstream string
	log      *slog.Logger

	connections, bytesUp, bytesDown          atomic.Int64
	dialsRefused, dialsTimedOut, dialsFailed atomic.Int64
}

// New returns a Proxy that forwards to upstream, a host:port dialled afresh
// for every accepted connection so the upstream can be restarted underneath
// it. Dial failures are logged to log.
func New(upstream string, log *slog.Logger) *Proxy {
	return &Proxy{upstream: upstream, log: log}
}

// Counters returns the traffic counted so far. It is safe to call while the
// proxy is serving.
func (p *Proxy) Counters() Counters {
	return Counters{
		Connections:   p.connections.Load(),
		BytesUp:       p.bytesUp.Load(),
		BytesDown:     p.bytesDown.Load(),
		DialsRefused:  p.dialsRefused.Load(),
		DialsTimedOut: p.dialsTimedOut.Load(),
		DialsFailed:   p.dialsFailed.Load(),
	}
}

// Serve accepts connections on ln until ctx is done or Accept fails. Either
// way it closes ln and every live connection and waits for them to finish
// before returning: nil when ctx is done, otherwise Accept's error, so a
// broken listener ends the process instead of leaving callers connecting into
// a backlog nobody accepts.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(ctx, func() { ln.Close() })

	for {
		caller, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		p.connections.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.forward(ctx, caller)
		}()
	}
}

// forward copies between caller and a new upstream connection until both
// directions reach EOF, either side fails, or ctx is done.
func (p *Proxy) forward(ctx context.Context, caller net.Conn) {
	defer caller.Close()
	upstream, err := (&net.Dialer{}).DialContext(ctx, "tcp", p.upstream)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.dialFailure(err).Add(1)
		p.log.Warn("dial upstream", "upstream", p.upstream, "err", err)
		return
	}
	defer upstream.Close()

	closeBoth := func() {
		caller.Close()
		upstream.Close()
	}
	stop := context.AfterFunc(ctx, closeBoth)
	defer stop()

	errc := make(chan error, 2)
	go func() { errc <- copyHalf(upstream, caller, &p.bytesUp) }()
	go func() { errc <- copyHalf(caller, upstream, &p.bytesDown) }()
	for range 2 {
		// A failed direction can't be half-closed, so the other one is cut too.
		if err := <-errc; err != nil {
			closeBoth()
		}
	}
}

// dialFailure returns the counter for a failed upstream dial.
func (p *Proxy) dialFailure(err error) *atomic.Int64 {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return &p.dialsRefused
	case errors.As(err, &ne) && ne.Timeout():
		return &p.dialsTimedOut
	default:
		return &p.dialsFailed
	}
}

// copyHalf copies src to dst, counting into n as bytes are read, then
// half-closes dst so the peer sees the same EOF.
func copyHalf(dst, src net.Conn, n *atomic.Int64) error {
	if _, err := io.Copy(dst, countingReader{src, n}); err != nil {
		return err
	}
	if hc, ok := dst.(interface{ CloseWrite() error }); ok {
		return hc.CloseWrite()
	}
	return dst.Close()
}

// countingReader adds each read's length to n before returning it.
type countingReader struct {
	io.Reader
	n *atomic.Int64
}

func (r countingReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.n.Add(int64(n))
	return n, err
}
