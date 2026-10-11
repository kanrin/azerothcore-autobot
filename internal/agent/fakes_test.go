package agent

import (
	"context"
	"testing"
	"time"
)

// fakeBehavior is a Behavior with settable score and outcome, instrumented so a
// test can assert what the agent did to it.
type fakeBehavior struct {
	name    string
	score   func(bb *Blackboard) int
	outcome func(bb *Blackboard) error

	runs  int
	stops int
}

func newFake(name string, score int, outcome error) *fakeBehavior {
	return &fakeBehavior{
		name:    name,
		score:   func(*Blackboard) int { return score },
		outcome: func(*Blackboard) error { return outcome },
	}
}

func (f *fakeBehavior) Name() string { return f.name }

func (f *fakeBehavior) Score(bb *Blackboard) int {
	if f.score == nil {
		return 0
	}
	return f.score(bb)
}

func (f *fakeBehavior) Run(_ context.Context, bb *Blackboard) error {
	f.runs++
	if f.outcome == nil {
		return nil
	}
	return f.outcome(bb)
}

func (f *fakeBehavior) Stop(context.Context) error {
	f.stops++
	return nil
}

// newTestAgent builds an agent whose clock the test drives by hand.
func newTestAgent(t *testing.T, now *time.Time, margin int, behaviors ...Behavior) *Agent {
	t.Helper()
	reg, err := NewRegistry(behaviors...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	a, err := New("testbot", reg, Options{
		Clock:         func() time.Time { return *now },
		PreemptMargin: margin,
		Backoff:       Backoff{Base: time.Second, Max: 8 * time.Second, Factor: 2},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}
