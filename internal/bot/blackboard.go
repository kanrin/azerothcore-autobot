package bot

import (
	"fmt"
	"time"

	"azerothcore-autobot/internal/agent"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// Blackboard keys shared by the runner and every behavior.
//
// They live here, next to the adapters, because this package is the one place
// that knows both sides: a feature package never imports another feature, and
// neither of them imports the runner's bookkeeping.
const (
	// SelfKey holds the bot's own state, republished every tick.
	SelfKey = "self"

	// DestinationKey holds a pending "go there" request.
	DestinationKey = "travel.destination"

	// ArrivalKey holds the last completed walk, so "did the bot arrive" is a fact
	// on the blackboard rather than something to infer from a log line.
	ArrivalKey = "travel.arrival"
)

// Self is the part of the bot's own state that a decision needs.
type Self struct {
	GUID       uint64
	MapID      uint32
	X, Y, Z    float32
	O          float32
	Health     uint32
	MaxHealth  uint32
	InCombat   bool
	TargetGUID uint64

	// InWorld is false while a teleport is in flight or the session is not
	// playing: behaviors must not act then, and must not trust the position.
	InWorld bool
}

// Position returns the position as a navmesh point.
func (s Self) Position() navigation.Point3D { return navigation.Point3D{X: s.X, Y: s.Y, Z: s.Z} }

// SelfOf reads the published self state.
func SelfOf(bb *agent.Blackboard) (Self, bool) {
	v, ok := bb.Get(SelfKey)
	if !ok {
		return Self{}, false
	}
	self, ok := v.(Self)
	return self, ok
}

// Destination is "walk to this point on this map".
type Destination struct {
	MapID   uint32
	X, Y, Z float32

	// Reason is why the bot is going there. It reaches the log and the arrival
	// record, which is what makes a route explainable after the fact.
	Reason string
}

// To returns the destination as a navmesh point.
func (d Destination) To() navigation.Point3D {
	return navigation.Point3D{X: d.X, Y: d.Y, Z: d.Z}
}

// String renders the destination for logs.
func (d Destination) String() string {
	return fmt.Sprintf("map %d (%.1f, %.1f, %.1f) %s", d.MapID, d.X, d.Y, d.Z, d.Reason)
}

// Arrival records a finished walk.
type Arrival struct {
	Destination Destination
	From        navigation.Point3D
	At          time.Time
}

// GoTo asks the bot to walk somewhere. Any behavior, and the CLI, may set it; the
// travel behavior picks it up on the next tick.
func GoTo(bb *agent.Blackboard, d Destination) { bb.Set(DestinationKey, d) }

// DestinationOf reads the pending walk request.
func DestinationOf(bb *agent.Blackboard) (Destination, bool) {
	v, ok := bb.Get(DestinationKey)
	if !ok {
		return Destination{}, false
	}
	d, ok := v.(Destination)
	return d, ok
}

// ClearGoTo cancels a pending walk.
func ClearGoTo(bb *agent.Blackboard) { bb.Delete(DestinationKey) }

// LastArrival returns the most recently completed walk.
func LastArrival(bb *agent.Blackboard) (Arrival, bool) {
	v, ok := bb.Get(ArrivalKey)
	if !ok {
		return Arrival{}, false
	}
	a, ok := v.(Arrival)
	return a, ok
}
