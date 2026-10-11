package travel

import (
	"errors"
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// TestStep_StuckReplansThenGivesUp checks the retry budget: a bot that cannot
// move gets replanned, and once the budget is spent the walker reports stuck
// instead of retrying forever.
func TestStep_StuckReplansThenGivesUp(t *testing.T) {
	planner := &fakePlanner{}
	mover := &fakeMover{}
	w := newTestWalker(t, planner, mover)
	now := time.Unix(1000, 0)
	stuck := navigation.Point3D{X: 1, Y: 1, Z: 0}
	// The route starts at the stuck position: a bot that does not move at all is
	// what the stuck rule is about.
	if _, _, err := w.Go(now, 0, stuck, navigation.Point3D{X: 100}, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}

	for replan := 1; replan <= 3; replan++ {
		now = now.Add(3 * time.Second)
		status, err := w.Step(now, stuck, 0, 0)
		if status != StatusReplanning || err != nil {
			t.Fatalf("replan %d = %v, %v; want StatusReplanning", replan, status, err)
		}
		if got := w.Route().Replans; got != replan {
			t.Fatalf("Replans = %d, want %d", got, replan)
		}
	}
	if len(mover.paths) != 4 {
		t.Errorf("mover got %d path(s), want 4 (the initial plan plus three replans)", len(mover.paths))
	}

	now = now.Add(3 * time.Second)
	status, err := w.Step(now, stuck, 0, 0)
	if status != StatusStuck || !errors.Is(err, ErrStuck) {
		t.Fatalf("Step = %v, %v; want StatusStuck wrapping ErrStuck", status, err)
	}
	if w.Route() != nil || mover.moving {
		t.Error("giving up left the route or the movement engine running")
	}
}

// TestStep_ProgressAvoidsReplanning checks the other side of the stuck rule:
// slow but real progress must not trigger a replan, however long it takes.
func TestStep_ProgressAvoidsReplanning(t *testing.T) {
	mover := &fakeMover{}
	w := newTestWalker(t, &fakePlanner{}, mover)
	now := time.Unix(1000, 0)
	if _, _, err := w.Go(now, 0, navigation.Point3D{}, navigation.Point3D{X: 100}, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}

	pos := navigation.Point3D{X: 1}
	for i := 0; i < 20; i++ {
		now = now.Add(3 * time.Second)
		status, err := w.Step(now, pos, 0, 0)
		if status != StatusMoving || err != nil {
			t.Fatalf("step %d = %v, %v; want StatusMoving while progress continues", i, status, err)
		}
		pos.X++ // exactly the minimum progress per stuck window
	}
	if got := w.Route().Replans; got != 0 {
		t.Errorf("Replans = %d, want 0 while the bot keeps moving", got)
	}
}

// TestStep_StuckReplanUnreachable covers a replan that cannot be planned: the
// walker reports unreachable rather than stuck, because the difference decides
// whether the caller retries elsewhere or gives up on the destination.
func TestStep_StuckReplanUnreachable(t *testing.T) {
	calls := 0
	planner := &fakePlanner{fn: func(uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		calls++
		if calls == 1 {
			return &navigation.PathResult{Found: true, Points: []navigation.Point3D{{}, {X: 100}}}, nil
		}
		return &navigation.PathResult{Found: false}, nil
	}}
	w := newTestWalker(t, planner, &fakeMover{})
	now := time.Unix(1000, 0)
	start := navigation.Point3D{X: 1}
	if _, _, err := w.Go(now, 0, start, navigation.Point3D{X: 100}, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}

	status, err := w.Step(now.Add(4*time.Second), start, 0, 0)
	if status != StatusUnreachable || !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Step = %v, %v; want StatusUnreachable wrapping ErrUnreachable", status, err)
	}
	if w.Route() != nil {
		t.Error("a failed replan left a route behind")
	}
}

// TestStop_IdleIsNoop keeps the shutdown path quiet: stopping an idle walker must
// not send a stop to an engine that never started moving.
func TestStop_IdleIsNoop(t *testing.T) {
	mover := &fakeMover{}
	w := newTestWalker(t, &fakePlanner{}, mover)
	w.Stop(time.Unix(1000, 0))
	if mover.stops != 0 {
		t.Errorf("mover.stops = %d, want 0 for an idle walker", mover.stops)
	}

	if _, _, err := w.Go(time.Unix(1000, 0), 0, navigation.Point3D{}, navigation.Point3D{X: 10}, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}
	w.Stop(time.Unix(1001, 0))
	if mover.stops != 1 || w.Route() != nil {
		t.Errorf("stops = %d, route = %v; want the movement stopped and the route dropped", mover.stops, w.Route())
	}
}
