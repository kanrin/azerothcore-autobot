package agent

// Phase is the agent's own lifecycle.
//
// It is deliberately a different enum from client.SessionPhase: the protocol
// phase answers "may I send gameplay packets at all", the agent phase answers
// "what is the bot trying to do". Mixing the two is how a bot ends up casting
// while a teleport is in flight.
type Phase string

// Agent phases. The names match the project rules (10-architecture.md).
const (
	PhaseIdle       Phase = "idle"       // nothing to do right now
	PhaseLoggingIn  Phase = "logging_in" // connecting, character select
	PhasePreparing  Phase = "preparing"  // buffed, at the start of a route
	PhasePlaying    Phase = "playing"    // questing, grinding, gathering
	PhaseDungeon    Phase = "dungeon"    // inside an instance
	PhaseRecovering Phase = "recovering" // after a death, a stuck route or a disconnect
	PhaseStopped    Phase = "stopped"    // shut down
)

// String renders the phase for logs.
func (p Phase) String() string {
	if p == "" {
		return string(PhaseIdle)
	}
	return string(p)
}

// Valid reports whether p is one of the defined phases.
func (p Phase) Valid() bool {
	switch p {
	case PhaseIdle, PhaseLoggingIn, PhasePreparing, PhasePlaying, PhaseDungeon, PhaseRecovering, PhaseStopped:
		return true
	default:
		return false
	}
}
