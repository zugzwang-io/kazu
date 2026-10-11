package payments

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/zugzwang-io/kazu/examples/shop/internal/db/dbtest"
)

func TestPGStoreRecordsOneChargePerKey(t *testing.T) {
	s := PGStore{DB: dbtest.Open(t, "payments")}
	ctx := context.Background()

	if _, err := s.Find(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Find before insert: %v, want ErrNotFound", err)
	}

	// Rounds of ten inserts for one key, all released at once, as from
	// duplicate submits.
	for round := range 10 {
		key := fmt.Sprintf("k%d", round)
		fire := make(chan struct{})
		errs := make(chan error, 10)
		var wg sync.WaitGroup
		for i := range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-fire
				c, err := s.InsertOnce(ctx, Charge{ID: fmt.Sprintf("ch_%d", i), OrderID: "ord_" + key, IdempotencyKey: key, AmountCents: 1500})
				if err == nil && c.IdempotencyKey != key {
					err = errors.New("returned another key's charge")
				}
				errs <- err
			}()
		}
		close(fire)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := s.DB.Pool.QueryRow(ctx, `select count(*) from charges where idempotency_key = $1`, key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s: %d charge rows, want 1", key, n)
		}
	}

	got, err := s.Find(ctx, "k0")
	if err != nil || got.OrderID != "ord_k0" || got.AmountCents != 1500 {
		t.Fatalf("Find: %+v %v", got, err)
	}
}
