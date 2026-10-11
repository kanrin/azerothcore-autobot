package wow

import (
	"context"
	"fmt"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// WaitFor blocks until cond returns true, ctx is done, or timeout elapses.
//
// desc names the thing being awaited and appears verbatim in the error, so a
// stuck run can be identified from one log line instead of a packet dump.
//
// cond runs on the calling goroutine and is re-evaluated every PollInterval. It
// must be cheap and must not send packets or block; read the client's exported
// getters instead.
func (s *Session) WaitFor(ctx context.Context, timeout time.Duration, desc string, cond func() bool) error {
	if cond() {
		return nil
	}

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wow: waiting for %s: %w", desc, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("wow: waiting for %s: %w", desc, ErrTimeout)
		case <-ticker.C:
			if cond() {
				return nil
			}
		}
	}
}

// WaitPhase blocks until the client reports want.
//
// AzerothGhost's own WaitForSessionPhase additionally keeps the socket alive
// through teleport phases, but it cannot be cancelled. This wrapper polls the
// exported Phase() getter so that shutdown is not blocked behind a waiter; use
// it for reconnect and assertion paths, and the client's login/logout channels
// for the login handshake.
func (s *Session) WaitPhase(ctx context.Context, want client.SessionPhase, timeout time.Duration) error {
	return s.WaitFor(ctx, timeout, "session phase "+want.String(), func() bool {
		return s.client.SessionPhase() == want
	})
}

// WaitInWorld blocks until the client is in the world.
func (s *Session) WaitInWorld(ctx context.Context, timeout time.Duration) error {
	return s.WaitFor(ctx, timeout, "session phase InWorld", s.client.IsInWorld)
}

// InWorld reports whether gameplay packets may be sent. Anything that moves,
// casts or attacks must check this first: packets sent in another phase are
// dropped by the server or get the connection closed.
func (s *Session) InWorld() bool { return s.client.IsInWorld() }

// Phase returns the current protocol session phase.
func (s *Session) Phase() client.SessionPhase { return s.client.SessionPhase() }

// CharGUID returns the logged-in character's GUID, or 0 before login.
func (s *Session) CharGUID() uint64 { return s.client.CharGUID() }

// Position returns the bot's world position and map id.
//
// After a cross-map teleport (FarTransfer) the object cache is cleared and the
// position is only trustworthy once the new map has been announced, so re-read
// it rather than caching it across a teleport.
func (s *Session) Position() (x, y, z, o float32, mapID uint32) {
	return s.client.Position()
}

// Health and MaxHealth are read from the live object cache, never from a cached
// copy: another player's client can kill the mob the bot is fighting, and a
// stale health value is the classic source of "attacking a corpse" loops.
func (s *Session) Health() uint32    { return s.client.Health() }
func (s *Session) MaxHealth() uint32 { return s.client.MaxHealth() }

// InCombat reports whether the bot is flagged as in combat.
func (s *Session) InCombat() bool { return s.client.InCombat() }

// TargetGUID returns the current selection, or 0 when nothing is selected.
func (s *Session) TargetGUID() uint64 { return s.client.TargetGUID() }

// Object returns the tracked object with the given GUID, or nil when it is not
// in the client's cache. A nil result means "not visible", never "does not
// exist": wait for the object rather than falling back to a remembered GUID.
func (s *Session) Object(guid uint64) *client.WorldObject { return s.client.GetObject(guid) }

// SetCharRace primes the race used for chat language selection. AzerothGhost
// needs it because a Horde character cannot speak the Common language used by
// default in GM-command chat.
func (s *Session) SetCharRace(race uint8) { s.client.SetCharRace(race) }
