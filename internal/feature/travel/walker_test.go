package travel

import (
	"errors"
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/navigation"
)

func TestNew_RejectsNils(t *testing.T) {
	if _, err := New(nil, &fakeMover{}, Options{}); err == nil {
		t.Error("New accepted a nil planner")
	}
	if _, err := New(&fakePlanner{}, nil, Options{}); err == nil {
		t.Error("New accepted a nil mover")
	}
}

// TestOptions_Defaults pins the tunables: they are what a stuck bot is judged
// against, so a silent change here would change behavior everywhere.
func TestOptions_Defaults(t *testing.T) {
	got := Options{}.withDefaults()
	if got.ArriveRadius != 2.0 || got.StuckAfter != 3*time.Second || got.MinProgress != 0.25 || got.MaxReplans != 3 {
		t.Errorf("defaults = %+v, want radius 2, stuck 3s, progress 0.25, 3 replans", got)
	}
}

// TestGo_PlansAndStartsMovement covers the happy path: the route is installed on
// the engine, and stepping keeps updating it.
func TestGo_PlansAndStartsMovement(t *testing.T) {
	planner := &fakePlanner{}
	mover := &fakeMover{}
	w := newTestWalker(t, planner, mover)
	start := time.Unix(1000, 0)
	to := navigation.Point3D{X: 10, Y: 0, Z: 0}

	route, status, err := w.Go(start, 0, navigation.Point3D{}, to, 1.5)
	if err != nil || status != StatusMoving {
		t.Fatalf("Go = %v, %v, %v; want a route and StatusMoving", route, status, err)
	}
	if route.Replans != 0 || route.To != to || route.MapID != 0 {
		t.Errorf("route = %+v, want a fresh route to %+v on map 0", route, to)
	}
	if len(mover.paths) != 1 || mover.inits != 1 {
		t.Errorf("mover got %d path(s) and %d init(s), want 1 and 1", len(mover.paths), mover.inits)
	}
	if w.Route() != route {
		t.Error("Route() does not return the installed route")
	}

	status, err = w.Step(start.Add(200*time.Millisecond), navigation.Point3D{X: 1, Y: 0}, 0, 1.5)
	if err != nil || status != StatusMoving {
		t.Fatalf("Step = %v, %v; want StatusMoving", status, err)
	}
	if mover.updates != 1 {
		t.Errorf("mover.updates = %d, want 1", mover.updates)
	}
}

// TestGo_Unreachable covers both ways a plan can fail: the planner errors, and
// the planner answers "no path".
func TestGo_Unreachable(t *testing.T) {
	boom := errors.New("mmap read failed")
	failing := &fakePlanner{fn: func(uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		return nil, boom
	}}
	from := navigation.Point3D{}
	to := navigation.Point3D{X: 5}

	w := newTestWalker(t, failing, &fakeMover{})
	if _, status, err := w.Go(time.Unix(1000, 0), 0, from, to, 0); status != StatusUnreachable || !errors.Is(err, boom) {
		t.Fatalf("Go = %v, %v; want StatusUnreachable wrapping the planner error", status, err)
	}
	if w.Route() != nil {
		t.Error("a failed plan left a route behind")
	}

	empty := &fakePlanner{fn: func(uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		return &navigation.PathResult{Found: false}, nil
	}}
	w = newTestWalker(t, empty, &fakeMover{})
	if _, status, err := w.Go(time.Unix(1000, 0), 0, from, to, 0); status != StatusUnreachable || !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Go = %v, %v; want StatusUnreachable wrapping ErrUnreachable", status, err)
	}
}

// TestStep_ArrivedAndIdle checks arrival: the engine is stopped, the route is
// dropped, and a further step reports idle instead of walking to nowhere.
func TestStep_ArrivedAndIdle(t *testing.T) {
	mover := &fakeMover{}
	w := newTestWalker(t, &fakePlanner{}, mover)
	now := time.Unix(1000, 0)
	to := navigation.Point3D{X: 10, Y: 10, Z: 0}
	if _, _, err := w.Go(now, 0, navigation.Point3D{}, to, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}

	status, err := w.Step(now.Add(time.Second), navigation.Point3D{X: 9, Y: 9.5, Z: 0}, 0, 0)
	if err != nil || status != StatusArrived {
		t.Fatalf("Step = %v, %v; want StatusArrived inside the arrival radius", status, err)
	}
	if mover.stops != 1 {
		t.Errorf("mover.stops = %d, want 1 on arrival", mover.stops)
	}
	if w.Route() != nil {
		t.Error("the route survived arrival")
	}
	if status, err := w.Step(now, to, 0, 0); status != StatusIdle || err != nil {
		t.Errorf("Step after arrival = %v, %v; want StatusIdle, nil", status, err)
	}
}

// TestStep_WrongMap covers the one thing walking cannot fix: the bot is on
// another map, which needs a teleport or a flight path.
func TestStep_WrongMap(t *testing.T) {
	mover := &fakeMover{}
	w := newTestWalker(t, &fakePlanner{}, mover)
	now := time.Unix(1000, 0)
	if _, _, err := w.Go(now, 1, navigation.Point3D{}, navigation.Point3D{X: 50}, 0); err != nil {
		t.Fatalf("Go: %v", err)
	}

	status, err := w.Step(now.Add(time.Second), navigation.Point3D{X: 1}, 0, 0)
	if status != StatusWrongMap || !errors.Is(err, ErrWrongMap) {
		t.Fatalf("Step = %v, %v; want StatusWrongMap wrapping ErrWrongMap", status, err)
	}
	if mover.stops != 1 || w.Route() != nil {
		t.Errorf("stops = %d, route = %v; want movement stopped and the route dropped", mover.stops, w.Route())
	}
}
