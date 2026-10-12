package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"azerothcore-autobot/internal/agent"
	"azerothcore-autobot/internal/feature/travel"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// fakeWalker plays back a scripted sequence of walker results, so the adapter's
// rules (plan, arrive, refuse a wrong map, give up, stop) are testable without
// mmaps or a server. travel.Walker itself is covered by its own package.
type fakeWalker struct {
	// steps is the queue of statuses Step returns, one per call. Once the queue is
	// empty, stepStatus/stepErr are returned every time (Moving and no error by
	// default), which is how a test models "this route never works".
	steps      []travel.Status
	errs       []error
	stepStatus travel.Status
	stepErr    error

	// planStatus and planErr are what Go returns.
	planStatus travel.Status
	planErr    error

	route   *travel.Route
	plans   int
	stepped int
	stopped int
}

var _ walker = (*fakeWalker)(nil)

func (f *fakeWalker) Route() *travel.Route { return f.route }

func (f *fakeWalker) Go(now time.Time, mapID uint32, from, to navigation.Point3D, o float32) (*travel.Route, travel.Status, error) {
	f.plans++
	if f.planErr != nil {
		return nil, f.planStatus, f.planErr
	}
	status := f.planStatus
	if status == 0 { // unset means "a normal plan"
		status = travel.StatusMoving
	}
	if status != travel.StatusMoving {
		return nil, status, nil
	}
	f.route = &travel.Route{MapID: mapID, To: to, PlannedAt: now, PlannedFrom: from}
	return f.route, status, nil
}

func (f *fakeWalker) Step(time.Time, navigation.Point3D, uint32, float32) (travel.Status, error) {
	f.stepped++
	if len(f.steps) == 0 {
		status := f.stepStatus
		if status == 0 { // unset means "a healthy step"
			status = travel.StatusMoving
		}
		f.clearOnTerminal(status)
		return status, f.stepErr
	}
	status := f.steps[0]
	f.steps = f.steps[1:]
	var err error
	if len(f.errs) > 0 {
		err = f.errs[0]
		f.errs = f.errs[1:]
	}
	f.clearOnTerminal(status)
	return status, err
}

// clearOnTerminal mirrors the real walker: every terminal status drops the route,
// so the next Run has to plan again.
func (f *fakeWalker) clearOnTerminal(status travel.Status) {
	if status != travel.StatusMoving && status != travel.StatusReplanning {
		f.route = nil
	}
}

func (f *fakeWalker) Stop(time.Time) {
	f.stopped++
	f.route = nil
}

// boardWith publishes a self state and, optionally, a destination.
func boardWith(self Self, dest *Destination) *agent.Blackboard {
	bb := agent.NewBlackboard("testbot")
	bb.Set(SelfKey, self)
	if dest != nil {
		GoTo(bb, *dest)
	}
	return bb
}

// elwynnWith builds a self state that is in the world near Northshire.
func elwynnWith() Self {
	return Self{
		GUID: 7, MapID: 0, X: -8949.9, Y: -132.5, Z: 83.5, O: 0.7,
		Health: 55, MaxHealth: 55, InWorld: true,
	}
}

// TestTravelBehavior_GivesUpAfterBoundedFailures covers the retry budget of one
// destination: the walker abandons the route each time, the behavior plans again,
// and after the bounded number of rounds the request itself is dropped instead of
// being retried until someone stops the bot by hand.
func TestTravelBehavior_GivesUpAfterBoundedFailures(t *testing.T) {
	fake := &fakeWalker{stepStatus: travel.StatusStuck, stepErr: travel.ErrStuck}
	b := newTravelBehavior(fake, discardLogger())
	dest := Destination{MapID: 0, X: -8850, Y: -190, Z: 90, Reason: "behind a wall"}
	bb := boardWith(elwynnWith(), &dest)

	for round := 1; round <= maxWalkFailures; round++ {
		if err := b.Run(context.Background(), bb); err != nil {
			t.Fatalf("round %d: planning again: %v", round, err)
		}

		err := b.Run(context.Background(), bb)
		if err == nil {
			t.Fatalf("round %d: walking into the wall returned nil, want an error", round)
		}
		if round < maxWalkFailures && !errors.Is(err, travel.ErrStuck) {
			t.Fatalf("round %d = %v, want travel.ErrStuck", round, err)
		}
		if round == maxWalkFailures && !strings.Contains(err.Error(), "gave up") {
			t.Fatalf("last round = %v, want it to say it gave up", err)
		}
	}

	if _, ok := DestinationOf(bb); ok {
		t.Error("the destination survived giving up")
	}
	if err := b.Run(context.Background(), bb); !errors.Is(err, agent.Done) {
		t.Errorf("Run after giving up = %v, want agent.Done", err)
	}
}

func TestTravelBehavior_ScoresZeroWithoutARequestOrInWorld(t *testing.T) {
	b := newTravelBehavior(&fakeWalker{}, discardLogger())

	// No destination at all.
	if score := b.Score(boardWith(elwynnWith(), nil)); score != 0 {
		t.Errorf("Score without a destination = %d, want 0", score)
	}

	// A destination, but a teleport is in flight: acting now would walk from a
	// position the server has already replaced.
	offWorld := elwynnWith()
	offWorld.InWorld = false
	dest := Destination{MapID: 0, X: -8902, Y: -162, Z: 82}
	if score := b.Score(boardWith(offWorld, &dest)); score != 0 {
		t.Errorf("Score while not in the world = %d, want 0", score)
	}

	// A destination and a bot that can walk: the normal case.
	if score := b.Score(boardWith(elwynnWith(), &dest)); score != travelScore {
		t.Errorf("Score with a pending walk = %d, want %d", score, travelScore)
	}
}

func TestTravelBehavior_StopReleasesTheRoute(t *testing.T) {
	fake := &fakeWalker{}
	b := newTravelBehavior(fake, discardLogger())
	dest := Destination{MapID: 0, X: -8902, Y: -162, Z: 82}
	bb := boardWith(elwynnWith(), &dest)

	if err := b.Run(context.Background(), bb); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if fake.stopped != 1 {
		t.Errorf("the motion engine was stopped %d times, want 1", fake.stopped)
	}
	if fake.Route() != nil {
		t.Error("the route survived a stop")
	}
}

func TestNew_ValidatesTheSpec(t *testing.T) {
	full := Spec{AuthAddr: "127.0.0.1:3724", Account: "autobot01", Password: "pw", Character: "Autobotone"}
	if _, err := New(full, Options{}); err != nil {
		t.Fatalf("New with a complete spec: %v", err)
	}

	cases := map[string]func(*Spec){
		"no auth address": func(s *Spec) { s.AuthAddr = "" },
		"no account":      func(s *Spec) { s.Account = "" },
		"no password":     func(s *Spec) { s.Password = "" },
		"no character":    func(s *Spec) { s.Character = "" },
	}
	for name, mutate := range cases {
		spec := full
		mutate(&spec)
		if _, err := New(spec, Options{}); err == nil {
			t.Errorf("New with %s = nil, want an error", name)
		}
	}
}

func TestSpecLabel(t *testing.T) {
	if got := (Spec{Name: "alpha", Character: "Autobotone"}).Label(); got != "alpha" {
		t.Errorf("Label = %q, want the bot name", got)
	}
	if got := (Spec{Character: "Autobotone"}).Label(); got != "Autobotone" {
		t.Errorf("Label = %q, want the character name when there is no bot name", got)
	}
}

func TestRegistry_TravelNeedsAWalker(t *testing.T) {
	r := &Runner{spec: Spec{Character: "Autobotone", Features: Features{Travel: true}}, opts: Options{}.withDefaults()}

	// The feature is on but no pathfinding was built: that is a programming
	// error, not something to discover on the first tick.
	if _, err := r.registry(); err == nil {
		t.Error("registry with travel enabled and no walker = nil, want an error")
	}

	r.walker = &fakeWalker{}
	reg, err := r.registry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if behaviors := reg.Behaviors(); len(behaviors) != 1 || behaviors[0].Name() != "travel" {
		t.Fatalf("behaviors = %v, want exactly the travel behavior", behaviors)
	}
}

// discardLogger keeps a test quiet without hiding a failure on the test's side.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTravelBehavior_PlansAndWalks(t *testing.T) {
	fake := &fakeWalker{steps: []travel.Status{travel.StatusMoving, travel.StatusMoving}}
	b := newTravelBehavior(fake, discardLogger())
	now := time.Unix(5000, 0)
	b.now = func() time.Time { return now }

	dest := Destination{MapID: 0, X: -8902.6, Y: -162.6, Z: 82.0, Reason: "quest giver"}
	bb := boardWith(elwynnWith(), &dest)

	// Tick 1: there is no route yet, so the behavior plans one.
	if err := b.Run(context.Background(), bb); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if fake.plans != 1 || fake.stepped != 0 {
		t.Fatalf("after planning: plans=%d steps=%d, want 1 and 0", fake.plans, fake.stepped)
	}
	if _, ok := DestinationOf(bb); !ok {
		t.Fatal("the destination was cleared while the walk was still planned")
	}

	// Ticks 2 and 3: the walker is asked to advance, reusing the same route.
	for i := range 2 {
		if err := b.Run(context.Background(), bb); err != nil {
			t.Fatalf("moving Run %d: %v", i+2, err)
		}
	}
	if fake.stepped != 2 {
		t.Errorf("steps = %d, want 2", fake.stepped)
	}
	if fake.plans != 1 {
		t.Errorf("plans = %d, want 1: a live route must not be replanned every tick", fake.plans)
	}
}

func TestTravelBehavior_ArrivalEndsTheRequest(t *testing.T) {
	fake := &fakeWalker{steps: []travel.Status{travel.StatusMoving, travel.StatusArrived}}
	b := newTravelBehavior(fake, discardLogger())
	now := time.Unix(6000, 0)
	b.now = func() time.Time { return now }

	dest := Destination{MapID: 0, X: -8902.6, Y: -162.6, Z: 82.0, Reason: "quest giver"}
	bb := boardWith(elwynnWith(), &dest)

	if err := b.Run(context.Background(), bb); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := b.Run(context.Background(), bb); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := b.Run(context.Background(), bb); !errors.Is(err, agent.Done) {
		t.Fatalf("arrival Run = %v, want agent.Done", err)
	}

	if _, ok := DestinationOf(bb); ok {
		t.Error("the destination survived the arrival")
	}
	arrival, ok := LastArrival(bb)
	if !ok {
		t.Fatal("no arrival was recorded on the blackboard")
	}
	if arrival.Destination != dest || !arrival.At.Equal(now) {
		t.Errorf("arrival = %+v, want the destination at %v", arrival, now)
	}
}

func TestTravelBehavior_WrongMapIsRefusedWithoutPlanning(t *testing.T) {
	fake := &fakeWalker{}
	b := newTravelBehavior(fake, discardLogger())

	// The bot is in Elwynn and the destination is in Durotar: walking cannot fix
	// that, and planning on the wrong map would produce a nonsense route.
	dest := Destination{MapID: 1, X: 1000, Y: -4000, Z: 90, Reason: "other continent"}
	bb := boardWith(elwynnWith(), &dest)

	err := b.Run(context.Background(), bb)
	if !errors.Is(err, travel.ErrWrongMap) {
		t.Fatalf("Run = %v, want travel.ErrWrongMap", err)
	}
	if fake.plans != 0 {
		t.Errorf("plans = %d, want 0 for a cross-map destination", fake.plans)
	}
}
