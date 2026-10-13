package balikobot

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"time"
)

// DefaultRetryBackoff is the delay before the first retry when
// RetryPolicy.BaseBackoff is zero.
const DefaultRetryBackoff = 200 * time.Millisecond

// maxRetryBackoff caps the doubling backoff of a RetryPolicy.
const maxRetryBackoff = 30 * time.Second

// RetryPolicy opts read-only calls into automatic retries. The zero value
// keeps the default behavior of exactly one HTTP attempt per call. The policy
// never applies to the mutating calls AddPackage, OrderBatch, DropPackage and
// OrderPickup; those always perform exactly one attempt.
//
// The retried calls are Branches, Overview, Labels, OrderViewLabels,
// TrackStatus, WhoAmI, ActivatedServices, Countries, COD, CarrierCapabilities
// and the label downloads.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the first.
	// Values below two disable retries.
	MaxAttempts int
	// BaseBackoff is the delay before the first retry. Every following retry
	// doubles the delay, capped at 30 seconds. Zero selects
	// DefaultRetryBackoff. A longer provider Retry-After hint always wins.
	BaseBackoff time.Duration
	// Jitter spreads every retry delay over a random interval from zero to
	// the computed delay.
	Jitter bool
}

func (policy RetryPolicy) backoff(attempt int) time.Duration {
	delay := min(policy.BaseBackoff, maxRetryBackoff)
	if delay <= 0 {
		delay = DefaultRetryBackoff
	}
	for index := 1; index < attempt && delay < maxRetryBackoff; index++ {
		delay = min(2*delay, maxRetryBackoff)
	}
	if !policy.Jitter {
		return delay
	}
	return time.Duration(rand.Float64() * float64(delay))
}

func (client *Client) readAttempts() int {
	return max(1, client.retryPolicy.MaxAttempts)
}

func (client *Client) waitRetry(ctx context.Context, err error, attempt int) error {
	delay := client.retryPolicy.backoff(attempt)
	var transient *TransientError
	if errors.As(err, &transient) && transient.RetryAfter > delay {
		delay = transient.RetryAfter
	}
	if client.sleep == nil {
		return nil
	}
	return client.sleep(ctx, delay)
}

func retryableForRead(err error) bool {
	return errors.Is(err, ErrUnavailable)
}

func retryRead[T any](
	client *Client,
	ctx context.Context,
	call func(context.Context) (T, error),
) (T, error) {
	var zero T
	attempts := client.readAttempts()
	for attempt := 1; ; attempt++ {
		value, err := call(ctx)
		if err == nil || attempt >= attempts || !retryableForRead(err) {
			return value, err
		}
		if waitErr := client.waitRetry(ctx, err, attempt); waitErr != nil {
			return zero, errors.Join(err, waitErr)
		}
	}
}

func (client *Client) retryStream[T any](
	ctx context.Context,
	w io.Writer,
	call func(context.Context, io.Writer) (T, error),
) (T, error) {
	var zero T
	attempts := client.readAttempts()
	for attempt := 1; ; attempt++ {
		counter := writeCounter{writer: w}
		value, err := call(ctx, &counter)
		if err == nil || attempt >= attempts || counter.written > 0 || !retryableForRead(err) {
			return value, err
		}
		if waitErr := client.waitRetry(ctx, err, attempt); waitErr != nil {
			return zero, errors.Join(err, waitErr)
		}
	}
}

type writeCounter struct {
	writer  io.Writer
	written int64
}

func (counter *writeCounter) Write(data []byte) (int, error) {
	count, err := counter.writer.Write(data)
	counter.written += int64(count)
	return count, err
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
