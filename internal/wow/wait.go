package wow

import (
	"context"
	"fmt"
	"time"
)

// waitUpstream runs a blocking, non-cancellable AzerothGhost waiter but returns
// as soon as ctx is done or timeout elapses.
//
// The goroutine cannot be killed because upstream waiters take no context, but
// every waiter used through this helper also unblocks when the client stops, so
// it exits promptly after Close and the leak stays bounded by the caller's own
// timeout.
func waitUpstream(ctx context.Context, desc string, timeout time.Duration, wait func(time.Duration) error) error {
	done := make(chan error, 1)
	go func() { done <- wait(timeout) }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("wow: %s: %w", desc, err)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("wow: %s: %w", desc, ErrTimeout)
	case <-ctx.Done():
		return fmt.Errorf("wow: %s: %w", desc, ctx.Err())
	}
}

// sleepCtx waits for d or until ctx is done, whichever comes first.
//
// This exists only for retry backoff between attempts. It must never stand in
// for a condition waiter: if there is a signal to wait for, use WaitFor or an
// Exchange instead.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
