// Package travel moves a bot along the ground: it plans a route with the
// navmesh and drives the motion engine one tick at a time.
//
// Why this package exists rather than calling the client directly: movement has
// to be checkable without a server. The walker depends on two narrow interfaces
// — a Planner (the mmap pathfinder) and a Mover (upstream's
// movement.MovementController, which owns heartbeats, turns and ground snapping)
// — so the arrival, wrong-map, replan and stuck rules are unit-testable, and a
// behavior only has to call Step once per tick.
//
// Scope: walking. Cross-continent travel needs flight paths, which this package
// deliberately does not implement; a route whose map id differs from the bot's is
// reported as such instead of walking off the edge of the world.
package travel

import (
	"errors"
	"fmt"
	"time"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// Sentinel errors. They mark the expected "cannot do that here" outcomes, which
// callers classify as preconditions rather than as infrastructure failures.
var (
	// ErrUnreachable means the planner could not produce a path to the target.
	ErrUnreachable = errors.New("travel: target unreachable")

	// ErrWrongMap means the bot is not on the route's map; walking cannot fix
	// that, a teleport or a flight path is needed.
	ErrWrongMap = errors.New("travel: bot is on a different map")

	// ErrStuck means every replan failed and the bot is not making progress.
	ErrStuck = errors.New("travel: stuck")
)

// Status is the outcome of one Step.
type Status uint8

// Step outcomes.
const (
	StatusIdle        Status = iota // no route: nothing to do
	StatusMoving                    // moving, keep calling Step
	StatusArrived                   // reached the destination, route cleared
	StatusReplanning                // progress stalled and a new path was planned
	StatusWrongMap                  // bot and route are on different maps
	StatusStuck                     // replans exhausted, movement stopped
	StatusUnreachable               // no path exists to the destination
)

// String renders the status for logs.
func (s Status) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusMoving:
		return "moving"
	case StatusArrived:
		return "arrived"
	case StatusReplanning:
		return "replanning"
	case StatusWrongMap:
		return "wrong_map"
	case StatusStuck:
		return "stuck"
	case StatusUnreachable:
		return "unreachable"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

// Planner is the pathfinder. *navigation.embeddedNavigator satisfies it; tests
// use a fake, so no mmap data is needed to exercise this package.
type Planner interface {
	FindPath(mapID uint32, start, dest navigation.Point3D) (*navigation.PathResult, error)
}

// Mover is the motion engine. *movement.MovementController satisfies it: the
// controller owns the movement packets, the heartbeat cadence and the turn
// handling, and is driven by being handed a path and then being told the time.
type Mover interface {
	// SetPath installs a path; the first point should be near the bot.
	SetPath(path []navigation.Point3D, startTime time.Time, initialO float32, mapID uint32)

	// Update advances the simulation to now and emits whatever movement the
	// engine decides is due.
	Update(now time.Time)

	// IsMoving reports whether the engine is still following a path.
	IsMoving() bool

	// Stop ends movement.
	Stop(now time.Time)

	// InitPositionFromWorld tells the engine where the server says the bot is.
	InitPositionFromWorld(x, y, z, o float32)
}

// Options are the walker's tunables. The zero value is usable.
type Options struct {
	// ArriveRadius is how close the bot has to get to the destination. It is
	// larger than the engine's waypoint radius because positions arrive with
	// network jitter.
	ArriveRadius float32

	// StuckAfter is how long the bot may fail to make progress before the route
	// is replanned.
	StuckAfter time.Duration

	// MinProgress is how far the bot has to move for the route to count as
	// progressing. It must be well below the distance covered in StuckAfter, or
	// slow but healthy movement looks stuck.
	MinProgress float32

	// MaxReplans bounds replanning, so a wall the pathfinder insists on walking
	// into ends as StatusStuck instead of an infinite loop.
	MaxReplans int
}

// withDefaults fills in the zero fields.
func (o Options) withDefaults() Options {
	if o.ArriveRadius <= 0 {
		o.ArriveRadius = 2.0
	}
	if o.StuckAfter <= 0 {
		o.StuckAfter = 3 * time.Second
	}
	if o.MinProgress <= 0 {
		o.MinProgress = 0.25
	}
	if o.MaxReplans <= 0 {
		o.MaxReplans = 3
	}
	return o
}

// Route is one plan, kept by the caller across ticks.
type Route struct {
	// MapID and To identify the destination; To is what arrival is measured
	// against, since path points are only a coarse approximation.
	MapID uint32
	To    navigation.Point3D

	// Points is the planned path, starting near where the bot was when it was
	// planned.
	Points []navigation.Point3D

	// PlannedAt, Replans and PlannedFrom record how the route came to be, which
	// is what makes a stuck route explainable after the fact.
	PlannedAt   time.Time
	PlannedFrom navigation.Point3D
	Replans     int
}
