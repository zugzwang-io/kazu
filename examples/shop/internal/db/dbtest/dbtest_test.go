package dbtest

import (
	"context"
	"testing"
)

// A service's pool sees only its own schema: an unqualified query naming
// another service's table fails, as it does in production.
func TestConnectSeesOnlyItsOwnSchema(t *testing.T) {
	base := Open(t, "payments", "checkout")
	checkout := Connect(t, base, "checkout")
	ctx := context.Background()
	if _, err := checkout.Pool.Exec(ctx, "select 1 from orders"); err != nil {
		t.Fatalf("own table: %v", err)
	}
	if _, err := checkout.Pool.Exec(ctx, "select 1 from charges"); err == nil {
		t.Fatal("checkout's pool read payments' charges table unqualified")
	}
}
