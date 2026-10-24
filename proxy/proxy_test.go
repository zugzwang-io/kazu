package proxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/proxy"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// serve starts handle on a local listener, one goroutine per connection, and
// returns the listener's address.
func serve(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln := listen(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handle(c)
			}()
		}
	}()
	return ln.Addr().String()
}

// echo copies everything back, then half-closes once the caller has.
func echo(c net.Conn) {
	_, _ = io.Copy(c, c)
	_ = c.(*net.TCPConn).CloseWrite()
}

// startProxy runs a Proxy to upstream until the test ends and returns it with
// its address.
func startProxy(t *testing.T, upstream string) (*proxy.Proxy, string) {
	t.Helper()
	ln := listen(t)
	p := proxy.New(upstream, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- p.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		waitServe(t, done)
	})
	return p, ln.Addr().String()
}

// waitServe fails the test unless Serve, already cancelled, returns nil soon.
func waitServe(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Serve still running 5s after cancel")
	}
}

// dial connects to addr with a deadline, so a proxy that fails to pass
// something through fails the test instead of hanging it.
func dial(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c.(*net.TCPConn)
}

func roundTrip(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != msg {
		t.Fatalf("echo = %q, want %q", got, msg)
	}
}

func TestCountersUpdateWhileConnectionIsOpen(t *testing.T) {
	p, addr := startProxy(t, serve(t, echo))
	c := dial(t, addr)

	roundTrip(t, c, "hello")
	if got, want := p.Counters(), (proxy.Counters{Connections: 1, BytesUp: 5, BytesDown: 5}); got != want {
		t.Fatalf("after one round trip: %+v, want %+v", got, want)
	}
	roundTrip(t, c, "again!")
	if got, want := p.Counters(), (proxy.Counters{Connections: 1, BytesUp: 11, BytesDown: 11}); got != want {
		t.Fatalf("after two round trips: %+v, want %+v", got, want)
	}
}

func TestCountsEveryConnection(t *testing.T) {
	p, addr := startProxy(t, serve(t, echo))
	for range 3 {
		roundTrip(t, dial(t, addr), "x")
	}
	if got, want := p.Counters(), (proxy.Counters{Connections: 3, BytesUp: 3, BytesDown: 3}); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

func TestPassesHalfCloseThrough(t *testing.T) {
	upstream := serve(t, func(c net.Conn) {
		req, _ := io.ReadAll(c)
		_, _ = c.Write(append([]byte("got "), req...))
	})
	_, addr := startProxy(t, upstream)
	c := dial(t, addr)

	if _, err := c.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	resp, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "got request" {
		t.Fatalf("response = %q, want %q", resp, "got request")
	}
}

func TestCallerResetClosesUpstream(t *testing.T) {
	ended := make(chan error, 1)
	upstream := serve(t, func(c net.Conn) {
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, err := io.Copy(io.Discard, c)
		ended <- err
	})
	_, addr := startProxy(t, upstream)
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := c.SetLinger(0); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-ended; isTimeout(err) {
		t.Fatalf("upstream connection still open after the caller reset: %v", err)
	}
}

func TestUpstreamDownClosesCallerAndKeepsServing(t *testing.T) {
	ln := listen(t)
	refused := ln.Addr().String()
	ln.Close()

	p, addr := startProxy(t, refused)
	for range 2 {
		n, err := dial(t, addr).Read(make([]byte, 1))
		if n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read from a proxy with no upstream = %d, %v; want 0, EOF", n, err)
		}
	}
	if got, want := p.Counters(), (proxy.Counters{Connections: 2, DialsRefused: 2}); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

func TestCountsAnUndialableUpstreamAsFailed(t *testing.T) {
	p, addr := startProxy(t, "127.0.0.1:99999") // an invalid port fails before any network I/O
	if _, err := dial(t, addr).Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read from a proxy with an invalid upstream address = %v, want EOF", err)
	}
	if got, want := p.Counters(), (proxy.Counters{Connections: 1, DialsFailed: 1}); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

func TestServeStopsAndClosesLiveConnections(t *testing.T) {
	ln := listen(t)
	p := proxy.New(serve(t, echo), slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- p.Serve(ctx, ln) }()

	c := dial(t, ln.Addr().String())
	roundTrip(t, c, "live")
	cancel()

	waitServe(t, done)
	if n, err := c.Read(make([]byte, 1)); n != 0 || isTimeout(err) {
		t.Fatalf("live connection still open after Serve returned: %d, %v", n, err)
	}
	if _, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String()); err == nil {
		t.Fatal("listener still accepting after Serve returned")
	}
}

func TestServeReturnsAcceptError(t *testing.T) {
	ln := listen(t)
	ln.Close()
	err := proxy.New("127.0.0.1:1", slog.New(slog.DiscardHandler)).Serve(context.Background(), ln)
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Serve on a closed listener = %v, want net.ErrClosed", err)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestServeWaitsForLiveConnections(t *testing.T) {
	ln := slowCloseListener{listen(t), make(chan struct{}, 1), make(chan struct{})}
	p := proxy.New(serve(t, echo), slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx, ln) }()

	roundTrip(t, dial(t, ln.Addr().String()), "live")
	cancel()
	<-ln.closing
	select {
	case <-done:
		t.Fatal("Serve returned while a connection was still closing")
	case <-time.After(100 * time.Millisecond):
	}
	close(ln.release)
	waitServe(t, done)
}

func TestAcceptFailureClosesLiveConnections(t *testing.T) {
	ln := &failingListener{Listener: listen(t), fail: make(chan struct{})}
	p := proxy.New(serve(t, echo), slog.New(slog.DiscardHandler))
	done := make(chan error, 1)
	go func() { done <- p.Serve(context.Background(), ln) }()

	c := dial(t, ln.Addr().String())
	roundTrip(t, c, "live")
	close(ln.fail)

	select {
	case err := <-done:
		if !errors.Is(err, errAccept) {
			t.Fatalf("Serve = %v, want %v", err, errAccept)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve still running 5s after Accept failed with a connection live")
	}
	if n, err := c.Read(make([]byte, 1)); n != 0 || isTimeout(err) {
		t.Fatalf("live connection still open after Serve returned: %d, %v", n, err)
	}
	if _, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String()); err == nil {
		t.Fatal("listener still accepting after Accept failed")
	}
}

var errAccept = errors.New("accept failed")

// failingListener passes its first Accept through, then fails every later one
// once fail is closed.
type failingListener struct {
	net.Listener
	fail     chan struct{}
	accepted atomic.Bool
}

func (l *failingListener) Accept() (net.Conn, error) {
	if l.accepted.CompareAndSwap(false, true) {
		return l.Listener.Accept()
	}
	<-l.fail
	return nil, errAccept
}

// slowCloseListener hands out connections whose Close signals closing, then
// waits for release before closing the socket.
type slowCloseListener struct {
	net.Listener
	closing, release chan struct{}
}

func (l slowCloseListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return slowCloseConn{c, l}, nil
}

type slowCloseConn struct {
	net.Conn
	l slowCloseListener
}

func (c slowCloseConn) Close() error {
	select {
	case c.l.closing <- struct{}{}:
	default:
	}
	<-c.l.release
	return c.Conn.Close()
}
