package agent

import (
	"context"
	"errors"
	"fmt"
)

// Done is what a behavior's Run returns when it finished its unit of work: the
// agent then forgets the behavior and re-scores everything next tick.
//
// A behavior that is still working (walking a route, waiting for a loot window)
// returns nil instead, so it keeps running while its score stays competitive.
var Done = errors.New("agent: behavior done")

// Behavior is one thing a bot can do: follow a quest, grind, gather, vendor.
//
// One behavior owns exactly one feature and must not import another feature
// package. Everything shared goes through the blackboard.
type Behavior interface {
	// Name identifies the behavior in logs and telemetry. It must be unique
	// inside a registry and non-empty.
	Name() string

	// Score says how much this behavior wants to run right now. Only scores
	// above zero are candidates; the higher score wins, and ties are broken by
	// registration order so a run is reproducible. Score is called on every
	// tick for every behavior, so it must be cheap: read the blackboard, do not
	// send packets or touch the filesystem.
	Score(bb *Blackboard) int

	// Run performs one step of work.
	//
	// Run must return promptly: it must never sleep and must never block on the
	// network. A step either finishes (return Done), makes progress and wants to
	// continue (return nil), or reports a failure. Anything that needs to wait
	// for a signal registers the wait on the blackboard and returns.
	Run(ctx context.Context, bb *Blackboard) error
}

// Stopper is implemented by behaviors that hold state which has to be released
// when they are preempted or when the agent shuts down: a pending packet
// exchange, an open loot window, a route the next behavior would inherit.
type Stopper interface {
	Stop(ctx context.Context) error
}

// Score is one behavior's current willingness to run.
type Score struct {
	Name  string
	Value int
}

// Registry holds the behaviors of one bot in registration order.
//
// Registration order matters twice: it breaks score ties deterministically, and
// it is the order the CLI prints in a run summary.
type Registry struct {
	behaviors []Behavior
	byName    map[string]Behavior
}

// NewRegistry builds a registry. An empty or duplicate name is a programming
// error and fails at startup rather than silently dropping a behavior.
func NewRegistry(behaviors ...Behavior) (*Registry, error) {
	reg := &Registry{byName: make(map[string]Behavior, len(behaviors))}
	for i, b := range behaviors {
		if b == nil {
			return nil, fmt.Errorf("agent: behavior %d is nil", i)
		}
		name := b.Name()
		if name == "" {
			return nil, fmt.Errorf("agent: behavior %d has an empty name", i)
		}
		if _, dup := reg.byName[name]; dup {
			return nil, fmt.Errorf("agent: duplicate behavior %q", name)
		}
		reg.behaviors = append(reg.behaviors, b)
		reg.byName[name] = b
	}
	return reg, nil
}

// Behaviors returns the registered behaviors in registration order.
func (r *Registry) Behaviors() []Behavior { return r.behaviors }

// Get looks a behavior up by name.
func (r *Registry) Get(name string) (Behavior, bool) {
	b, ok := r.byName[name]
	return b, ok
}

// Scores returns every behavior's current score, in registration order. It is
// what a run summary prints, and what makes "the bot is idle because everything
// scored zero" explainable.
func (r *Registry) Scores(bb *Blackboard) []Score {
	scores := make([]Score, 0, len(r.behaviors))
	for _, b := range r.behaviors {
		scores = append(scores, Score{Name: b.Name(), Value: b.Score(bb)})
	}
	return scores
}

// Best returns the highest-scoring candidate: the first behavior with the
// highest score above zero, or nil when nothing wants to run.
func (r *Registry) Best(bb *Blackboard) (Behavior, int) {
	var best Behavior
	bestScore := 0
	for _, b := range r.behaviors {
		score := b.Score(bb)
		if score > bestScore {
			best, bestScore = b, score
		}
	}
	return best, bestScore
}
