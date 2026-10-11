package agent

import (
	"context"
	"testing"
	"time"
)

// TestRun_StopsOnCancel checks the shutdown contract: cancelling the context is
// how the caller stops a bot, Run returns nil for it, the running behavior is
// released and the agent ends up in the stopped phase.
//
// The test waits for the behavior's first run before cancelling, so it does not
// depend on how many ticks fit into a sleep.
func TestRun_StopsOnCancel(t *testing.T) {
	now := time.Unix(1000, 0)
	ran := make(chan struct{}, 1)
	looper := newFake("looper", 50, nil)
	looper.outcome = func(*Blackboard) error {
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil
	}

	a := newTestAgent(t, &now, 25, looper)
	a.opts.Tick = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the behavior never ran")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil for a cancelled context", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}

	if got := a.Blackboard().Phase(); got != PhaseStopped {
		t.Errorf("phase = %s, want %s", got, PhaseStopped)
	}
	if looper.stops != 1 {
		t.Errorf("looper.stops = %d, want 1: the running behavior must be released", looper.stops)
	}
	if a.Current() != nil {
		t.Errorf("Current = %v, want nil after shutdown", a.Current().Name())
	}
	if a.Blackboard().Tick() == 0 {
		t.Error("no tick ran before the context was cancelled")
	}
}

// TestStop_ReleasesBehaviorWithoutStoppingTheLoop covers the caller-driven
// shutdown path: a command stops the running behavior before logging out, while
// the agent itself stays usable.
func TestStop_ReleasesBehaviorWithoutStoppingTheLoop(t *testing.T) {
	now := time.Unix(1000, 0)
	looper := newFake("looper", 50, nil)
	a := newTestAgent(t, &now, 25, looper)

	if _, err := a.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	a.Stop(context.Background())
	if looper.stops != 1 {
		t.Errorf("looper.stops = %d, want 1", looper.stops)
	}
	if a.Current() != nil {
		t.Errorf("Current = %v, want nil after Stop", a.Current().Name())
	}
	// The agent is still usable: the next tick starts the behavior again.
	if ran, _ := a.Tick(context.Background()); ran != looper {
		t.Fatalf("Tick after Stop ran %v, want looper", ran.Name())
	}
	if looper.runs != 2 {
		t.Errorf("looper.runs = %d, want 2", looper.runs)
	}
}
