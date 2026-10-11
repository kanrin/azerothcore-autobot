package travel

import (
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/navigation"
)

// fakePlanner returns a fixed path. By default it answers with a two-point path
// straight to the destination, which is all the walker needs to be exercised.
type fakePlanner struct {
	fn       func(mapID uint32, from, dest navigation.Point3D) (*navigation.PathResult, error)
	requests []navigation.Point3D
}

func (f *fakePlanner) FindPath(mapID uint32, from, dest navigation.Point3D) (*navigation.PathResult, error) {
	f.requests = append(f.requests, dest)
	if f.fn != nil {
		return f.fn(mapID, from, dest)
	}
	return &navigation.PathResult{Found: true, Points: []navigation.Point3D{from, dest}}, nil
}

// fakeMover records what the walker asked the motion engine to do.
type fakeMover struct {
	paths   [][]navigation.Point3D
	updates int
	stops   int
	inits   int
	moving  bool
}

func (f *fakeMover) SetPath(path []navigation.Point3D, _ time.Time, _ float32, _ uint32) {
	f.paths = append(f.paths, path)
	f.moving = true
}

func (f *fakeMover) Update(time.Time) { f.updates++ }

func (f *fakeMover) IsMoving() bool { return f.moving }

func (f *fakeMover) Stop(time.Time) {
	f.stops++
	f.moving = false
}

func (f *fakeMover) InitPositionFromWorld(_, _, _, _ float32) { f.inits++ }

// newTestWalker builds a walker with the project's tunables: a tight arrival
// radius, a three-second stuck window and three replans.
func newTestWalker(t *testing.T, planner Planner, mover Mover) *Walker {
	t.Helper()
	w, err := New(planner, mover, Options{
		ArriveRadius: 2,
		StuckAfter:   3 * time.Second,
		MinProgress:  0.25,
		MaxReplans:   3,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}
