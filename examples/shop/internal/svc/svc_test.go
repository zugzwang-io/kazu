package svc

import (
	"context"
	"testing"
	"time"
)

func TestDetachOutlivesTheCallerButNotForever(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := Detach(parent)
	defer cancel()
	cancelParent()
	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context ended with its caller: %v", err)
	}
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > DetachTimeout {
		t.Fatalf("deadline %v, %v; want one within %v", dl, ok, DetachTimeout)
	}
}
