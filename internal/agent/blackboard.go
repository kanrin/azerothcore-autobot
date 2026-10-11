// Package agent runs one bot: it keeps a blackboard of what the bot currently
// knows and picks, tick by tick, which behavior should act.
//
// The split is deliberate. Behaviors (internal/feature/*) own one piece of
// gameplay each and know nothing about each other; the agent owns priority,
// preemption and failure backoff; the protocol layer only sends packets. A
// behavior that needs to wait for a packet returns and registers the wait on the
// blackboard, so a tick never blocks.
package agent

import (
	"sync"
	"time"

	"azerothcore-autobot/internal/wow/state"
)

// Blackboard is the state one bot's behaviors share: what the agent knows right
// now plus a namespaced scratch area for feature-private values.
//
// Every method is safe for concurrent use. Behaviors must read through it rather
// than caching protocol state, because a teleport clears the client's object
// cache and a cached GUID then points at nothing.
type Blackboard struct {
	mu sync.RWMutex

	bot      string
	phase    Phase
	tick     int64
	now      time.Time
	snapshot state.Snapshot
	values   map[string]any
}

// NewBlackboard creates an empty blackboard for one bot.
func NewBlackboard(bot string) *Blackboard {
	return &Blackboard{bot: bot, phase: PhaseIdle, values: map[string]any{}}
}

// Bot returns the bot name this blackboard belongs to, which is what shows up in
// every log line and telemetry event.
func (bb *Blackboard) Bot() string { return bb.bot }

// Phase returns the agent phase. It is not the protocol phase: use the session
// for that.
func (bb *Blackboard) Phase() Phase {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	return bb.phase
}

// SetPhase records the agent phase.
func (bb *Blackboard) SetPhase(p Phase) {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	bb.phase = p
}

// Tick returns how many ticks the agent has run, which makes "did anything
// progress" answerable without a timer.
func (bb *Blackboard) Tick() int64 {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	return bb.tick
}

// Now returns the tick's timestamp. The agent sets it once per tick so every
// behavior in a tick sees the same clock, and tests can drive time by hand.
func (bb *Blackboard) Now() time.Time {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	return bb.now
}

// Snapshot returns the last observed self state: money, inventory, skills.
// A zero snapshot means "not known yet" — for example before login or right
// after a cross-map teleport — never "the bags are empty".
func (bb *Blackboard) Snapshot() state.Snapshot {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	return bb.snapshot
}

// SetSnapshot publishes a fresh self state.
func (bb *Blackboard) SetSnapshot(s state.Snapshot) {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	bb.snapshot = s
}

// Get reads a feature-private value.
func (bb *Blackboard) Get(key string) (any, bool) {
	bb.mu.RLock()
	defer bb.mu.RUnlock()
	v, ok := bb.values[key]
	return v, ok
}

// Set writes a feature-private value. Keys should be namespaced by feature
// ("gather.route"), because a flat namespace is shared by every behavior.
func (bb *Blackboard) Set(key string, value any) {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	bb.values[key] = value
}

// Delete removes a feature-private value.
func (bb *Blackboard) Delete(key string) {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	delete(bb.values, key)
}

// nextTick advances the clock and the counter. The agent calls it at the top of
// every tick so behaviors cannot disagree about the time.
func (bb *Blackboard) nextTick(now time.Time) int64 {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	bb.now = now
	bb.tick++
	return bb.tick
}
