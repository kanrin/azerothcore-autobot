package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"azerothcore-autobot/internal/agent"
	"azerothcore-autobot/internal/feature/travel"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// Scores. Travel sits above "nothing to do" and below the survival behaviors
// that arrive with combat: a bot that is about to die should interrupt a walk,
// but a pending walk must always start.
const (
	travelScore = 40

	// maxWalkFailures bounds the retries of one destination. The agent's backoff
	// bounds how often a failing behavior runs; without a bound on the count, a
	// destination the pathfinder cannot reach would be retried until the bot is
	// stopped by hand.
	maxWalkFailures = 3
)

// walker is the part of travel.Walker this package uses, narrowed so the adapter
// can be tested without mmaps. *travel.Walker satisfies it.
type walker interface {
	Route() *travel.Route
	Go(now time.Time, mapID uint32, from, to navigation.Point3D, o float32) (*travel.Route, travel.Status, error)
	Step(now time.Time, pos navigation.Point3D, mapID uint32, o float32) (travel.Status, error)
	Stop(now time.Time)
}

// travelBehavior walks the bot to the destination on the blackboard, one step per
// tick.
//
// Arrival, the wrong map, a stuck route and an unreachable one all end the
// request, so a caller is never left wondering whether the bot is still walking.
type travelBehavior struct {
	walker walker
	logger *slog.Logger

	// now is the clock the walker is driven by. It defaults to the wall clock
	// rather than the agent's tick clock, because both the motion engine's
	// heartbeats and the stuck detection are about real elapsed time; a test
	// injects a fake so nothing has to sleep.
	now func() time.Time

	// Bookkeeping of the current destination's failures.
	destKey  string
	failures int
}

// Name identifies the behavior in logs and telemetry.
func (b *travelBehavior) Name() string { return "travel" }

// Score wants to run whenever a walk is pending and the bot is able to walk.
func (b *travelBehavior) Score(bb *agent.Blackboard) int {
	if _, ok := DestinationOf(bb); !ok {
		return 0
	}
	if self, ok := SelfOf(bb); ok && !self.InWorld {
		return 0
	}
	return travelScore
}

// Run takes one step toward the destination.
func (b *travelBehavior) Run(_ context.Context, bb *agent.Blackboard) error {
	dest, ok := DestinationOf(bb)
	if !ok {
		return agent.Done
	}

	self, ok := SelfOf(bb)
	if !ok {
		return errors.New("travel: no self state published yet")
	}
	if !self.InWorld {
		return errors.New("travel: the bot is not in the world")
	}

	if b.walker.Route() == nil {
		return b.start(dest, self)
	}

	now := b.now()
	status, err := b.walker.Step(now, self.Position(), self.MapID, self.O)
	switch status {
	case travel.StatusMoving, travel.StatusReplanning:
		return nil
	case travel.StatusArrived:
		bb.Set(ArrivalKey, Arrival{Destination: dest, From: self.Position(), At: now})
		ClearGoTo(bb)
		b.forget()
		b.logger.Info("travel: arrived", "dest", dest.String(),
			"x", self.X, "y", self.Y, "z", self.Z)
		return agent.Done
	case travel.StatusIdle:
		// The route disappeared without arriving (stopped from outside): report
		// done so the next tick plans from where the bot really is.
		return agent.Done
	default:
		if err == nil {
			err = fmt.Errorf("travel: %s", status)
		}
		return b.fail(bb, dest, err)
	}
}

// start plans the first route for a destination.
func (b *travelBehavior) start(dest Destination, self Self) error {
	if self.MapID != dest.MapID {
		// Walking cannot cross maps; a flight path or a teleport has to happen
		// first, and saying so is more useful than planning on the wrong map.
		return fmt.Errorf("%w: destination %s, bot on map %d",
			travel.ErrWrongMap, dest.String(), self.MapID)
	}

	now := b.now()
	_, status, err := b.walker.Go(now, self.MapID, self.Position(), dest.To(), self.O)
	if err != nil {
		return err
	}
	if status != travel.StatusMoving {
		return fmt.Errorf("travel: planning returned %s", status)
	}

	b.logger.Info("travel: walking", "dest", dest.String(),
		"from_x", self.X, "from_y", self.Y, "from_z", self.Z)
	return nil
}

// Stop releases the route when the behavior is preempted or the agent shuts
// down: the next behavior inherits a bot that is standing still, not one still
// following a path.
func (b *travelBehavior) Stop(context.Context) error {
	b.walker.Stop(b.now())
	return nil
}

// fail counts a failure against the current destination and gives up after
// maxWalkFailures, so an impossible destination ends instead of being retried
// forever.
func (b *travelBehavior) fail(bb *agent.Blackboard, dest Destination, err error) error {
	key := fmt.Sprintf("%d/%.0f/%.0f", dest.MapID, dest.X, dest.Y)
	if key != b.destKey {
		b.forget()
		b.destKey = key
	}
	b.failures++
	if b.failures < maxWalkFailures {
		return err
	}

	ClearGoTo(bb)
	b.forget()
	return fmt.Errorf("%w (gave up on %s after %d attempts)", err, dest.String(), maxWalkFailures)
}

// forget resets the failure bookkeeping.
func (b *travelBehavior) forget() { b.destKey, b.failures = "", 0 }

// newTravelBehavior builds the adapter with the wall clock; a test overwrites
// now to drive it without sleeping.
func newTravelBehavior(w walker, logger *slog.Logger) *travelBehavior {
	return &travelBehavior{walker: w, logger: logger, now: time.Now}
}

// registry builds this runner's behaviors.
//
// It is the only place where a feature switch becomes a behavior, so adding a
// milestone means adding one case here and nothing else.
func (r *Runner) registry() (*agent.Registry, error) {
	var behaviors []agent.Behavior

	if r.spec.Features.Travel {
		if r.walker == nil {
			return nil, errors.New("feature travel is enabled but no pathfinding is configured")
		}
		behaviors = append(behaviors, newTravelBehavior(r.walker, r.opts.Logger))
	}

	if len(behaviors) == 0 {
		r.opts.Logger.Warn("bot: no behavior is enabled; the bot will log in and stand still",
			"bot", r.spec.Label())
	}
	return agent.NewRegistry(behaviors...)
}
