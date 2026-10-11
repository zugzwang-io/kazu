package checkout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"syscall"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// Outcome of charging an order, as far as checkout can tell.
type Outcome int

const (
	// OutcomeUnknown: payments may or may not have charged (a timeout, a reset,
	// a 5xx). The order stays pending.
	OutcomeUnknown Outcome = iota
	OutcomePaid
	// OutcomeDeclined: definitely not charged (a 4xx, or every attempt was
	// refused or shed). The order fails.
	OutcomeDeclined
)

// PaymentsClient calls payments' POST /charges with a per-attempt timeout and
// retries with full-jitter exponential backoff, always with the order's own
// idempotency key.
type PaymentsClient struct {
	URL            string // e.g. http://payments:8081
	HTTP           *http.Client
	AttemptTimeout time.Duration // 1s
	MaxAttempts    int           // 3
	BaseBackoff    time.Duration // 100ms
}

type attemptResult int

const (
	resultPaid         attemptResult = iota
	resultDeclined                   // 4xx: retrying won't change the answer
	resultNotProcessed               // refused or shed (503): safe to retry, nothing happened
	resultAmbiguous                  // timeout, reset, other 5xx: may have charged
)

var errRetry = errors.New("retry")

func (p *PaymentsClient) Charge(ctx context.Context, o Order) Outcome {
	var last attemptResult
	ambiguous := false
	op := func() error {
		last = p.attempt(ctx, o)
		switch last {
		case resultPaid:
			return nil
		case resultDeclined:
			return backoff.Permanent(errRetry)
		case resultAmbiguous:
			ambiguous = true
		}
		return errRetry
	}
	b := backoff.WithContext(backoff.WithMaxRetries(&fullJitter{base: p.BaseBackoff, max: 2 * time.Second}, uint64(p.MaxAttempts-1)), ctx)
	_ = backoff.Retry(op, b)

	switch {
	case last == resultPaid:
		return OutcomePaid
	case last == resultDeclined:
		return OutcomeDeclined
	case last == resultNotProcessed && !ambiguous && ctx.Err() == nil:
		return OutcomeDeclined
	default:
		return OutcomeUnknown
	}
}

func (p *PaymentsClient) attempt(ctx context.Context, o Order) attemptResult {
	ctx, cancel := context.WithTimeout(ctx, p.AttemptTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"order_id": o.ID, "amount_cents": o.AmountCents})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL+"/charges", bytes.NewReader(body))
	if err != nil {
		return resultDeclined
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", o.IdempotencyKey)

	resp, err := p.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return resultNotProcessed
		}
		return resultAmbiguous
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode/100 == 2:
		return resultPaid
	case resp.StatusCode == http.StatusServiceUnavailable:
		return resultNotProcessed // payments sheds before doing any work
	case resp.StatusCode/100 == 4:
		return resultDeclined
	default:
		return resultAmbiguous
	}
}

// fullJitter is exponential backoff with full jitter: the n-th wait is uniform
// in [0, min(max, base·2ⁿ)].
type fullJitter struct {
	base, max time.Duration
	n         int
}

func (b *fullJitter) NextBackOff() time.Duration {
	d := b.base << b.n
	if d > b.max || d <= 0 {
		d = b.max
	}
	b.n++
	return time.Duration(rand.Int64N(int64(d) + 1))
}

func (b *fullJitter) Reset() { b.n = 0 }
