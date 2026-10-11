package travel

import (
	"fmt"
	"time"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// Walker drives one bot along one route at a time, one step per agent tick.
//
// It holds no session and sends nothing itself: the motion engine does the
// talking, and the caller passes in the position it just read. That is what makes
// the whole thing testable without a server, and it also means the walker can
// never disagree with the session about where the bot is.
type Walker struct {
	planner Planner
	mover   Mover
	opts    Options

	route *Route

	// Progress bookkeeping for the stuck rule.
	lastPos    navigation.Point3D
	progressAt time.Time
	hasPos     bool
}

// New builds a walker. A nil planner or mover is a programming error and fails
// here rather than panicking on the first tick.
func New(planner Planner, mover Mover, opts Options) (*Walker, error) {
	if planner == nil {
		return nil, fmt.Errorf("travel: nil planner")
	}
	if mover == nil {
		return nil, fmt.Errorf("travel: nil mover")
	}
	return &Walker{planner: planner, mover: mover, opts: opts.withDefaults()}, nil
}

// Route returns the active route, or nil when the walker is idle.
func (w *Walker) Route() *Route { return w.route }

// Go plans a route and starts moving.
//
// from is where the caller believes the bot is, mapID and o are the map and the
// orientation to start from. A planner error or an empty path is ErrUnreachable:
// the caller picks another destination, it does not retry the same one.
func (w *Walker) Go(now time.Time, mapID uint32, from, to navigation.Point3D, o float32) (*Route, Status, error) {
	res, err := w.planner.FindPath(mapID, from, to)
	if err != nil {
		w.Stop(now)
		return nil, StatusUnreachable, fmt.Errorf("travel: plan route to %+v on map %d: %w", to, mapID, err)
	}
	if res == nil || !res.Found || len(res.Points) == 0 {
		w.Stop(now)
		return nil, StatusUnreachable, fmt.Errorf("travel: no path from %+v to %+v on map %d: %w",
			from, to, mapID, ErrUnreachable)
	}
	w.install(now, mapID, from, to, res.Points, 0, o)
	return w.route, StatusMoving, nil
}

// Step advances the route: call it once per agent tick.
//
// pos, mapID and o come from the session, so arrival is decided on the server's
// position and not on the engine's interpolation. The walker clears its route on
// arrival, on a wrong map and when it gives up, so the caller never has to.
func (w *Walker) Step(now time.Time, pos navigation.Point3D, mapID uint32, o float32) (Status, error) {
	if w.route == nil {
		return StatusIdle, nil
	}

	if mapID != w.route.MapID {
		routeMap := w.route.MapID
		w.Stop(now)
		return StatusWrongMap, fmt.Errorf("%w: route targets map %d, bot is on map %d",
			ErrWrongMap, routeMap, mapID)
	}

	if pos.DistanceTo2D(w.route.To) <= w.opts.ArriveRadius {
		w.Stop(now)
		return StatusArrived, nil
	}

	if !w.hasPos || pos.DistanceTo2D(w.lastPos) >= w.opts.MinProgress {
		w.lastPos, w.progressAt, w.hasPos = pos, now, true
	} else if now.Sub(w.progressAt) >= w.opts.StuckAfter {
		return w.replan(now, pos, mapID, o)
	}

	w.mover.Update(now)
	return StatusMoving, nil
}

// Stop aborts movement and forgets the route. It is safe to call when idle.
func (w *Walker) Stop(now time.Time) {
	if w.route == nil {
		return
	}
	w.mover.Stop(now)
	w.route = nil
}

// install replaces the route, hands the path to the motion engine and resets the
// progress bookkeeping.
func (w *Walker) install(now time.Time, mapID uint32, from, to navigation.Point3D,
	points []navigation.Point3D, replans int, o float32) {
	w.route = &Route{
		MapID:       mapID,
		To:          to,
		Points:      points,
		PlannedAt:   now,
		PlannedFrom: from,
		Replans:     replans,
	}
	w.lastPos, w.progressAt, w.hasPos = from, now, true
	w.mover.InitPositionFromWorld(from.X, from.Y, from.Z, o)
	w.mover.SetPath(points, now, o, mapID)
}

// replan handles a stalled route: plan again from where the bot actually is, or
// give up once the retry budget is spent.
func (w *Walker) replan(now time.Time, pos navigation.Point3D, mapID uint32, o float32) (Status, error) {
	route := w.route
	if route.Replans >= w.opts.MaxReplans {
		w.Stop(now)
		return StatusStuck, fmt.Errorf("%w: no progress for %s, %d replan(s) toward %+v",
			ErrStuck, w.opts.StuckAfter, route.Replans, route.To)
	}

	res, err := w.planner.FindPath(mapID, pos, route.To)
	if err != nil {
		w.Stop(now)
		return StatusUnreachable, fmt.Errorf("travel: replan toward %+v: %w", route.To, err)
	}
	if res == nil || !res.Found || len(res.Points) == 0 {
		w.Stop(now)
		return StatusUnreachable, fmt.Errorf("travel: replan toward %+v: %w", route.To, ErrUnreachable)
	}
	w.install(now, mapID, pos, route.To, res.Points, route.Replans+1, o)
	return StatusReplanning, nil
}
