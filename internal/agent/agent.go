package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// failure records one behavior's consecutive failures and the moment it may run
// again.
type failure struct {
	attempts int
	readyAt  time.Time
}

// Agent runs one bot: every tick it scores the registered behaviors and runs the
// winner.
type Agent struct {
	bot  string
	reg  *Registry
	bb   *Blackboard
	opts Options

	mu       sync.Mutex
	current  string // name of the behavior that ran last, "" for none
	failures map[string]*failure
}

// New builds an agent for one bot. The registry may hold every behavior the bot
// can ever run; a behavior decides for itself whether it applies right now by its
// score.
func New(bot string, reg *Registry, opts Options) (*Agent, error) {
	if reg == nil {
		return nil, errors.New("agent: nil registry")
	}
	return &Agent{
		bot:      bot,
		reg:      reg,
		bb:       NewBlackboard(bot),
		opts:     opts.withDefaults(),
		failures: map[string]*failure{},
	}, nil
}

// Blackboard returns the shared state behaviors read and write.
func (a *Agent) Blackboard() *Blackboard { return a.bb }

// Registry returns the behavior registry.
func (a *Agent) Registry() *Registry { return a.reg }

// Current returns the behavior that ran on the last tick, or nil.
func (a *Agent) Current() Behavior {
	name := a.currentName()
	if name == "" {
		return nil
	}
	b, ok := a.reg.Get(name)
	if !ok {
		return nil
	}
	return b
}

// Tick runs one iteration: advance the clock, score everything, pick a winner,
// run it.
//
// The returned behavior is the one that ran (nil when nothing wanted to run) and
// the error is whatever Run returned, so a caller can assert on both. A Run error
// is already logged and already put the behavior into backoff; the tick loop
// ignores it on purpose.
func (a *Agent) Tick(ctx context.Context) (Behavior, error) {
	now := a.opts.Clock()
	a.bb.nextTick(now)

	if a.opts.Refresh != nil {
		a.opts.Refresh(ctx, a.bb)
	}

	behaviors := a.reg.Behaviors()
	scores := make([]int, len(behaviors))
	for i, b := range behaviors {
		if a.blocked(b.Name(), now) {
			continue // a behavior in backoff scores zero, i.e. is not a candidate
		}
		scores[i] = b.Score(a.bb)
	}

	currentIndex := a.indexOf(behaviors, a.currentName())
	currentScore := 0
	if currentIndex >= 0 {
		currentScore = scores[currentIndex]
	}

	bestIndex, bestScore := -1, 0
	for i, score := range scores {
		if score > bestScore {
			bestIndex, bestScore = i, score
		}
	}

	// Hysteresis: keep the running behavior unless another one wins by the
	// preemption margin, or the running one stopped being a candidate at all (it
	// scored zero, or it is in backoff because it just failed).
	chosenIndex := bestIndex
	if currentIndex >= 0 && currentScore > 0 {
		if bestIndex < 0 || bestIndex == currentIndex || bestScore < currentScore+a.opts.PreemptMargin {
			chosenIndex = currentIndex
		}
	}

	if currentIndex >= 0 && chosenIndex != currentIndex {
		a.stopBehavior(ctx, behaviors[currentIndex])
		if chosenIndex < 0 {
			// The running behavior stopped being a candidate and nothing else wants
			// to run: the bot goes idle. That is a normal state (it is what happens
			// after a behavior returns Done), not a failure — and it must not be
			// logged by indexing the behavior list with -1.
			a.opts.Logger.Info("agent: no behavior wants to run", "bot", a.bot,
				"stopped", behaviors[currentIndex].Name(), "stopped_score", currentScore)
		} else {
			a.opts.Logger.Info("agent: switching behavior", "bot", a.bot,
				"from", behaviors[currentIndex].Name(), "from_score", currentScore,
				"to", behaviors[chosenIndex].Name(), "to_score", bestScore)
		}
	}

	if chosenIndex < 0 {
		a.setCurrent("")
		a.bb.SetPhase(PhaseIdle)
		return nil, nil
	}

	chosen := behaviors[chosenIndex]
	if chosenIndex != currentIndex {
		a.opts.Logger.Info("agent: starting behavior", "bot", a.bot,
			"behavior", chosen.Name(), "score", scores[chosenIndex])
	}
	a.setCurrent(chosen.Name())

	err := a.callRun(ctx, chosen, a.bb)
	switch {
	case err == nil:
		// Progress resets the failure counter: the backoff is about consecutive
		// failures, not about a behavior having ever failed.
		a.clearFailure(chosen.Name())
		return chosen, nil
	case errors.Is(err, Done):
		a.clearFailure(chosen.Name())
		a.setCurrent("")
		return chosen, nil
	default:
		delay := a.recordFailure(chosen.Name(), now)
		a.opts.Logger.Warn("agent: behavior failed", "bot", a.bot, "behavior", chosen.Name(),
			"err", err.Error(), "retry_in", delay.String())
		return chosen, err
	}
}

// callRun runs one behavior and turns a panic inside it into an error.
//
// A resident bot runs for hours, and one buggy behavior must not take the whole
// process down with it — in a fleet process that would stop every other bot too.
// The panic is logged with its stack so the bug stays visible, and the behavior
// then goes into backoff like any other failure.
func (a *Agent) callRun(ctx context.Context, b Behavior, bb *Blackboard) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		a.opts.Logger.Error("agent: behavior panicked", "bot", a.bot, "behavior", b.Name(),
			"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		err = fmt.Errorf("agent: behavior %s panicked: %v", b.Name(), recovered)
	}()
	return b.Run(ctx, bb)
}
