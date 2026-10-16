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
	if left := time.Until(dl); !ok || left > DetachTimeout || left < DetachTimeout/2 {
		t.Fatalf("deadline %v away (set: %v); want about %v", left, ok, DetachTimeout)
	}
}
