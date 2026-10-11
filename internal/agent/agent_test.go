package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewRegistry_RejectsBadBehaviors(t *testing.T) {
	if _, err := NewRegistry(nil); err == nil {
		t.Error("NewRegistry(nil) accepted a nil behavior")
	}
	if _, err := NewRegistry(newFake("", 1, nil)); err == nil {
		t.Error("NewRegistry accepted an empty name")
	}
	if _, err := NewRegistry(newFake("a", 1, nil), newFake("a", 1, nil)); err == nil {
		t.Error("NewRegistry accepted a duplicate name")
	}
}

// TestRegistry_Best checks the two selection rules: only positive scores are
// candidates, and ties go to registration order so runs are reproducible.
func TestRegistry_Best(t *testing.T) {
	low := newFake("low", 5, nil)
	high := newFake("high", 50, nil)

	reg, err := NewRegistry(newFake("zero", 0, nil), low, high, newFake("negative", -10, nil))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	bb := NewBlackboard("testbot")
	if best, score := reg.Best(bb); best.Name() != "high" || score != 50 {
		t.Errorf("Best = %v/%d, want high/50", best.Name(), score)
	}

	allZero, err := NewRegistry(newFake("zero", 0, nil), newFake("negative", -10, nil))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if best, score := allZero.Best(bb); best != nil || score != 0 {
		t.Errorf("Best = %v/%d, want nil/0 when nothing scores", best, score)
	}

	tie, err := NewRegistry(newFake("first", 30, nil), newFake("second", 30, nil))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if best, _ := tie.Best(bb); best.Name() != "first" {
		t.Errorf("Best = %v, want the first registered behavior on a tie", best.Name())
	}

	scores := reg.Scores(bb)
	want := []Score{{"zero", 0}, {"low", 5}, {"high", 50}, {"negative", -10}}
	if len(scores) != len(want) {
		t.Fatalf("Scores = %v, want %v", scores, want)
	}
	for i := range want {
		if scores[i] != want[i] {
			t.Errorf("Scores[%d] = %v, want %v", i, scores[i], want[i])
		}
	}
}

// TestTick_RunsTheWinner is the base case: the highest score runs, and a behavior
// that keeps wanting to run keeps running.
func TestTick_RunsTheWinner(t *testing.T) {
	winner := newFake("winner", 40, nil)
	other := newFake("other", 10, nil)
	now := time.Unix(1000, 0)
	a := newTestAgent(t, &now, 25, other, winner)

	ran, err := a.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if ran != winner {
		t.Fatalf("Tick ran %v, want winner", ran.Name())
	}
	if _, err := a.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if winner.runs != 2 || other.runs != 0 {
		t.Errorf("runs = winner %d other %d, want 2 and 0", winner.runs, other.runs)
	}
	if got := a.Blackboard().Tick(); got != 2 {
		t.Errorf("blackboard tick = %d, want 2", got)
	}
}

// TestTick_NoCandidateIdles covers a bot with nothing to do: the phase becomes
// idle and no behavior runs.
func TestTick_NoCandidateIdles(t *testing.T) {
	now := time.Unix(1000, 0)
	a := newTestAgent(t, &now, 25, newFake("idle", 0, nil))

	ran, err := a.Tick(context.Background())
	if err != nil || ran != nil {
		t.Fatalf("Tick = %v, %v; want nil, nil", ran, err)
	}
	if got := a.Blackboard().Phase(); got != PhaseIdle {
		t.Errorf("phase = %s, want %s", got, PhaseIdle)
	}
}

// TestTick_HysteresisAndPreemption checks that a slightly better behavior does
// not interrupt the running one, and that a decisively better one does, with the
// interrupted behavior told to stop.
func TestTick_HysteresisAndPreemption(t *testing.T) {
	now := time.Unix(1000, 0)
	runner := newFake("runner", 80, nil)
	challenger := newFake("challenger", 70, nil)
	a := newTestAgent(t, &now, 25, runner, challenger)

	// No behavior is running yet, so the highest score starts.
	if ran, _ := a.Tick(context.Background()); ran != runner {
		t.Fatalf("first Tick ran %v, want runner", ran.Name())
	}

	// The challenger becomes better, but not by the preemption margin: the agent
	// keeps the running behavior instead of thrashing between the two.
	challenger.score = func(*Blackboard) int { return 90 }
	if ran, _ := a.Tick(context.Background()); ran != runner {
		t.Fatalf("second Tick ran %v, want runner to keep running", ran.Name())
	}
	if runner.stops != 0 {
		t.Errorf("runner.stops = %d, want 0 while it is not preempted", runner.stops)
	}

	// Decisively better: preemption, and the interrupted behavior is released.
	challenger.score = func(*Blackboard) int { return 200 }
	if ran, _ := a.Tick(context.Background()); ran != challenger {
		t.Fatalf("third Tick ran %v, want challenger", ran.Name())
	}
	if runner.stops != 1 {
		t.Errorf("runner.stops = %d, want 1 after preemption", runner.stops)
	}
}

// TestTick_FailureErrorIsReturned makes sure a failure reaches the caller, which
// is what run summaries and e2e assertions read.
func TestTick_FailureErrorIsReturned(t *testing.T) {
	now := time.Unix(1000, 0)
	boom := errors.New("boom")
	broken := newFake("broken", 100, boom)
	a := newTestAgent(t, &now, 25, broken)

	ran, err := a.Tick(context.Background())
	if ran != broken || !errors.Is(err, boom) {
		t.Fatalf("Tick = %v, %v; want broken and the error", ran, err)
	}
}
