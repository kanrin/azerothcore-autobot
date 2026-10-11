package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoff_Delay(t *testing.T) {
	cases := []struct {
		name    string
		backoff Backoff
		attempt int
		want    time.Duration
	}{
		{"first attempt is the base", Backoff{Base: time.Second, Max: time.Minute, Factor: 2}, 1, time.Second},
		{"second doubles", Backoff{Base: time.Second, Max: time.Minute, Factor: 2}, 2, 2 * time.Second},
		{"third doubles again", Backoff{Base: time.Second, Max: time.Minute, Factor: 2}, 3, 4 * time.Second},
		{"capped at max", Backoff{Base: time.Second, Max: 3 * time.Second, Factor: 2}, 9, 3 * time.Second},
		{"zero attempt behaves like one", Backoff{Base: time.Second, Max: time.Minute, Factor: 2}, 0, time.Second},
		{"factor one never grows", Backoff{Base: 500 * time.Millisecond, Max: time.Minute, Factor: 1}, 5, 500 * time.Millisecond},
		{"zero value uses the defaults", Backoff{}, 2, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.backoff.Delay(tc.attempt); got != tc.want {
				t.Errorf("Delay(%d) = %s, want %s", tc.attempt, got, tc.want)
			}
		})
	}
}

// TestOptions_Defaults pins the cadence the project runs at: 200ms ticks, the
// same interval upstream's bot uses, and a preemption margin that is high enough
// to stop two similar behaviors from alternating.
func TestOptions_Defaults(t *testing.T) {
	got := Options{}.withDefaults()
	if got.Tick != 200*time.Millisecond {
		t.Errorf("Tick = %s, want 200ms", got.Tick)
	}
	if got.PreemptMargin != 25 {
		t.Errorf("PreemptMargin = %d, want 25", got.PreemptMargin)
	}
	if got.Backoff != DefaultBackoff {
		t.Errorf("Backoff = %+v, want %+v", got.Backoff, DefaultBackoff)
	}
	if got.Clock == nil || got.Logger == nil {
		t.Error("Clock and Logger must be filled in")
	}
	if _, err := New("bot", nil, Options{}); err == nil {
		t.Error("New accepted a nil registry")
	}
}

// TestTick_BackoffAfterFailure checks the reliability rule the project requires
// of every retry: an error puts the behavior into a growing backoff instead of
// letting it hammer the socket, and a later success clears it.
func TestTick_BackoffAfterFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	boom := errors.New("boom")
	broken := newFake("broken", 100, boom)
	fallback := newFake("fallback", 5, nil)
	a := newTestAgent(t, &now, 25, broken, fallback)

	// t0: the best behavior fails and is put into a 1s backoff.
	if ran, err := a.Tick(context.Background()); ran != broken || !errors.Is(err, boom) {
		t.Fatalf("first Tick = %v, %v; want broken and the error", ran, err)
	}
	// Still inside the backoff: it is not a candidate even though it scores
	// highest, so the next behavior runs instead.
	if ran, err := a.Tick(context.Background()); ran != fallback || err != nil {
		t.Fatalf("second Tick = %v, %v; want fallback, nil", ran, err)
	}
	if broken.runs != 1 {
		t.Errorf("broken.runs = %d, want 1 during backoff", broken.runs)
	}

	// t0+2s: past the 1s backoff, it runs again and fails again (2s backoff now).
	now = now.Add(2 * time.Second)
	if ran, err := a.Tick(context.Background()); ran != broken || !errors.Is(err, boom) {
		t.Fatalf("third Tick = %v, %v; want broken again after the backoff", ran, err)
	}

	// t0+5s: past the 2s backoff, and this time it succeeds, which clears the
	// failure counter.
	now = now.Add(3 * time.Second)
	broken.outcome = nil
	if ran, err := a.Tick(context.Background()); ran != broken || err != nil {
		t.Fatalf("fourth Tick = %v, %v; want broken, nil", ran, err)
	}

	if got := a.opts.Backoff.Delay(1); got != time.Second {
		t.Errorf("Delay(1) = %s, want 1s", got)
	}
	if got := a.opts.Backoff.Delay(4); got != 8*time.Second {
		t.Errorf("Delay(4) = %s, want the 8s cap of this test's backoff", got)
	}
}

// TestTick_DoneClearsTheBehavior checks that finishing a unit of work makes the
// behavior forgettable, so a one-shot behavior is re-scored from scratch.
func TestTick_DoneClearsTheBehavior(t *testing.T) {
	now := time.Unix(1000, 0)
	once := newFake("once", 100, Done)
	a := newTestAgent(t, &now, 25, once)

	if ran, err := a.Tick(context.Background()); ran != once || err != nil {
		t.Fatalf("first Tick = %v, %v; want once, nil (Done is not a failure)", ran, err)
	}
	if a.Current() != nil {
		t.Errorf("Current = %v, want nil after Done", a.Current().Name())
	}
	if ran, _ := a.Tick(context.Background()); ran != once {
		t.Fatalf("second Tick ran %v, want once again", ran.Name())
	}
	if once.runs != 2 {
		t.Errorf("once.runs = %d, want 2", once.runs)
	}
}

// TestBlackboard covers the shared state behaviors agree on.
func TestBlackboard(t *testing.T) {
	bb := NewBlackboard("bot7")
	if bb.Bot() != "bot7" {
		t.Errorf("Bot = %q, want bot7", bb.Bot())
	}
	if bb.Phase() != PhaseIdle {
		t.Errorf("initial phase = %s, want %s", bb.Phase(), PhaseIdle)
	}
	bb.SetPhase(PhasePlaying)
	if bb.Phase() != PhasePlaying {
		t.Errorf("phase = %s, want %s", bb.Phase(), PhasePlaying)
	}
	if _, ok := bb.Get("gather.route"); ok {
		t.Error("Get returned a value for an unset key")
	}
	bb.Set("gather.route", []uint32{1, 2})
	if v, ok := bb.Get("gather.route"); !ok || len(v.([]uint32)) != 2 {
		t.Errorf("Get = %v, %v; want the two-element route", v, ok)
	}
	bb.Delete("gather.route")
	if _, ok := bb.Get("gather.route"); ok {
		t.Error("Delete left the key behind")
	}
	if !PhaseDungeon.Valid() || Phase("nonsense").Valid() {
		t.Error("Phase.Valid disagrees with the defined phases")
	}
	if got := Phase("").String(); got != string(PhaseIdle) {
		t.Errorf("empty phase renders as %q, want %q", got, PhaseIdle)
	}
}
