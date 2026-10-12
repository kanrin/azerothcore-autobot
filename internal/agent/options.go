package agent

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// Backoff describes how a failing behavior's retry delay grows. A behavior that
// fails every tick would otherwise spin against the socket; a behavior that was
// merely unlucky (its target was stolen by another player) has to come back
// quickly. The cap is what keeps a broken behavior from silently disappearing
// for minutes.
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
}

// DefaultBackoff is 1s, doubling, capped at 30s.
var DefaultBackoff = Backoff{Base: time.Second, Max: 30 * time.Second, Factor: 2}

// withDefaults fills in the fields of a zero Backoff.
func (b Backoff) withDefaults() Backoff {
	if b.Base <= 0 {
		b.Base = DefaultBackoff.Base
	}
	if b.Max <= 0 {
		b.Max = DefaultBackoff.Max
	}
	if b.Factor < 1 {
		b.Factor = DefaultBackoff.Factor
	}
	return b
}

// Delay returns how long a behavior that has failed attempt times in a row must
// wait before it may run again. Attempts are 1-based and the result is capped at
// Max, so the delay never grows without bound.
func (b Backoff) Delay(attempt int) time.Duration {
	b = b.withDefaults()
	if attempt < 1 {
		attempt = 1
	}
	delay := b.Base
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * b.Factor)
		if delay >= b.Max {
			return b.Max
		}
	}
	return delay
}

// Options configures an Agent. The zero value is usable: 200ms ticks (the same
// cadence upstream's bot uses), a preemption margin of 25 score points and the
// default backoff.
type Options struct {
	// Tick is the interval between iterations of Run.
	Tick time.Duration

	// PreemptMargin is how much better another behavior's score has to be before
	// it interrupts the running one. Without a margin, two behaviors with similar
	// scores would alternate every tick and neither would make progress.
	PreemptMargin int

	// Backoff grows the delay after consecutive failures of one behavior.
	Backoff Backoff

	// Clock is the time source; nil means time.Now. Tests set it to drive the
	// backoff without sleeping.
	Clock func() time.Time

	// Refresh runs at the top of every tick, before anything is scored.
	//
	// It is where the caller publishes what the behaviors read: the self state
	// snapshot, the position, the keep-alive. Reading is cheap — the values come
	// from the client's cached update fields, no packet is sent — and doing it
	// once per tick is what keeps every behavior in a tick looking at the same
	// world instead of each re-reading a slightly newer one.
	Refresh func(ctx context.Context, bb *Blackboard)

	// Logger receives behavior switches, failures and shutdown. Nil discards.
	Logger *slog.Logger
}

// withDefaults returns a copy with every zero field filled in.
func (o Options) withDefaults() Options {
	if o.Tick <= 0 {
		o.Tick = 200 * time.Millisecond
	}
	if o.PreemptMargin <= 0 {
		o.PreemptMargin = 25
	}
	o.Backoff = o.Backoff.withDefaults()
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return o
}
