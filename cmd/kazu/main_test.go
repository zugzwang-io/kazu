package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsBadArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, usage},
		{"unknown command", []string{"prox"}, `unknown command "prox"`},
		{"no upstream", []string{"proxy", "-listen", ":0"}, usage},
		{"no listen", []string{"proxy", "-upstream", "a:1"}, usage},
		{"stray argument", []string{"proxy", "-listen", ":0", "-upstream", "a:1", "extra"}, usage},
		{"unknown flag", []string{"proxy", "-port", "1"}, "flag provided but not defined: -port"},
		{"bad listen address", []string{"proxy", "-listen", "nonsense", "-upstream", "a:1"}, "missing port in address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(t.Context(), tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run(%q) = %v, want an error containing %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestProxyHelpIsNotAnError(t *testing.T) {
	var out strings.Builder
	if err := run(t.Context(), []string{"proxy", "-h"}, &out); err != nil {
		t.Fatalf("run(proxy -h) = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "-upstream") {
		t.Fatalf("help output %q doesn't list the flags", out.String())
	}
}

func TestProxyForwardsUntilCancelled(t *testing.T) {
	upstream, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("hi"))
	}()

	ctx, cancel := context.WithCancel(t.Context())
	logR, logW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"proxy", "-listen", "127.0.0.1:0", "-upstream", upstream.Addr().String()}, logW)
	}()

	line, err := bufio.NewReader(logR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`msg="proxy listening" listen=(\S+) upstream=`).FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("first log line %q doesn't say where the proxy listens", line)
	}
	go func() { _, _ = io.Copy(io.Discard, logR) }()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "hi" {
		t.Fatalf("read through proxy = %q, %v; want %q", got, err, "hi")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run after cancel = %v, want nil", err)
	}
}
