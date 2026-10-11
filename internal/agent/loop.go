package agent

import (
	"context"
	"time"
)

// Run ticks until the context is cancelled.
//
// A cancelled context is how the caller asks the agent to stop, so Run returns
// nil after shutting the running behavior down: a non-nil return would mean an
// internal error, and the caller's exit code should not depend on whether it
// stopped the bot or the bot stopped itself.
func (a *Agent) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.opts.Tick)
	defer ticker.Stop()

	a.opts.Logger.Info("agent: running", "bot", a.bot, "tick", a.opts.Tick.String(),
		"behaviors", len(a.reg.Behaviors()))

	for {
		select {
		case <-ctx.Done():
			a.shutdown(ctx)
			return nil
		case <-ticker.C:
			_, _ = a.Tick(ctx) // Tick logs its own failures
		}
	}
}

// Stop releases the running behavior without stopping the loop. Callers use it
// when they shut a bot down themselves — before logging out, for instance — so
// that a half-finished wait does not outlive the session.
func (a *Agent) Stop(ctx context.Context) {
	name := a.currentName()
	if name == "" {
		return
	}
	if b, ok := a.reg.Get(name); ok {
		a.stopBehavior(ctx, b)
	}
	a.setCurrent("")
}

// shutdown stops the current behavior and marks the agent stopped.
func (a *Agent) shutdown(ctx context.Context) {
	a.Stop(ctx)
	a.bb.SetPhase(PhaseStopped)
	a.opts.Logger.Info("agent: stopped", "bot", a.bot, "ticks", a.bb.Tick())
}

// stopBehavior tells a behavior it is being interrupted, if it can be told.
func (a *Agent) stopBehavior(ctx context.Context, b Behavior) {
	stopper, ok := b.(Stopper)
	if !ok {
		return
	}
	if err := stopper.Stop(ctx); err != nil {
		a.opts.Logger.Warn("agent: behavior stop failed", "bot", a.bot,
			"behavior", b.Name(), "err", err.Error())
	}
}

// blocked reports whether the behavior is still in backoff.
func (a *Agent) blocked(name string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.failures[name]
	if !ok {
		return false
	}
	return now.Before(f.readyAt)
}

// recordFailure counts one failure and returns the delay that now applies.
func (a *Agent) recordFailure(name string, now time.Time) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.failures[name]
	if !ok {
		f = &failure{}
		a.failures[name] = f
	}
	f.attempts++
	delay := a.opts.Backoff.Delay(f.attempts)
	f.readyAt = now.Add(delay)
	return delay
}

// clearFailure forgets a behavior's failures after it reports Done.
func (a *Agent) clearFailure(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, name)
}

// currentName returns the name of the behavior that ran last.
func (a *Agent) currentName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// setCurrent records the behavior that ran last.
func (a *Agent) setCurrent(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = name
}

// indexOf finds a behavior by name. Names, not interface values, are compared:
// registry names are unique by construction, and comparing interfaces would
// panic on a behavior implemented by a non-comparable type.
func (a *Agent) indexOf(behaviors []Behavior, name string) int {
	if name == "" {
		return -1
	}
	for i, b := range behaviors {
		if b.Name() == name {
			return i
		}
	}
	return -1
}
