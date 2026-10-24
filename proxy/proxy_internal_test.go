package proxy

import (
	"errors"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestDialFailureClassifiesErrors(t *testing.T) {
	connect := func(errno error) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
	}
	p := &Proxy{}
	for _, tc := range []struct {
		name string
		err  error
		want *atomic.Int64
	}{
		{"refused", connect(syscall.ECONNREFUSED), &p.dialsRefused},
		{"connect timed out", connect(syscall.ETIMEDOUT), &p.dialsTimedOut},
		{"no route", connect(syscall.EHOSTUNREACH), &p.dialsFailed},
		{"name not found", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", IsNotFound: true}}, &p.dialsFailed},
		{"other", errors.New("boom"), &p.dialsFailed},
	} {
		if got := p.dialFailure(tc.err); got != tc.want {
			t.Errorf("%s: counted under the wrong counter", tc.name)
		}
	}
}
