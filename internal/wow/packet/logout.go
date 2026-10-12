package packet

import "fmt"

// LogoutResponse is SMSG_LOGOUT_RESPONSE, the server's answer to
// CMSG_LOGOUT_REQUEST.
//
// AC: src/server/game/Server/Packets/CharacterPackets.cpp:30-35,
// LogoutResponse::Write:
//
//	_worldPacket << uint32(LogoutResult);
//	_worldPacket << uint8(Instant);
//
// Reason is zero when the logout was accepted. The non-zero values come from
// MiscHandler.cpp:435-441 and mean the request was refused; in that case the
// server does NOT send SMSG_LOGOUT_COMPLETE at all, so a client that only waits
// for completion ends up reporting a timeout instead of the real reason.
//
// Instant says whether the server logged the character out straight away (a
// resting area, a taxi, or the instant-logout permission). When it is false the
// server waits out the countdown in WorldSession.h:547 — `currTime >=
// _logoutTime + 20` — before sending SMSG_LOGOUT_COMPLETE.
type LogoutResponse struct {
	Reason  uint32
	Instant bool
}

// Logout refusal reasons, from MiscHandler.cpp:435-441.
const (
	// LogoutReasonNone means the request was accepted.
	LogoutReasonNone uint32 = 0

	// LogoutReasonInCombat is sent while the player is in combat.
	LogoutReasonInCombat uint32 = 1

	// LogoutReasonBlocked covers duelling, being AFK in a sanctuary while
	// AFK.PreventLogout is on, and being frozen by a GM.
	LogoutReasonBlocked uint32 = 2

	// LogoutReasonFalling is sent while the player is falling or jumping.
	LogoutReasonFalling uint32 = 3
)

// String renders the reason for logs.
func (r LogoutResponse) String() string {
	return fmt.Sprintf("%s (reason %d, instant=%t)", LogoutReasonName(r.Reason), r.Reason, r.Instant)
}

// LogoutReasonName names a refusal reason, falling back to a hex rendering for
// values newer servers may add.
func LogoutReasonName(reason uint32) string {
	switch reason {
	case LogoutReasonNone:
		return "accepted"
	case LogoutReasonInCombat:
		return "in combat"
	case LogoutReasonBlocked:
		return "blocked (duel, AFK in a sanctuary, or frozen)"
	case LogoutReasonFalling:
		return "falling or jumping"
	default:
		return fmt.Sprintf("unknown reason 0x%X", reason)
	}
}

// ParseLogoutResponse decodes SMSG_LOGOUT_RESPONSE.
//
// The payload is five bytes; anything shorter is a truncated packet and is
// reported rather than guessed at.
func ParseLogoutResponse(data []byte) (LogoutResponse, error) {
	r := NewReader(data)
	reason, err := r.Uint32()
	if err != nil {
		return LogoutResponse{}, fmt.Errorf("packet: SMSG_LOGOUT_RESPONSE: %w", err)
	}
	instant, err := r.Bool()
	if err != nil {
		return LogoutResponse{}, fmt.Errorf("packet: SMSG_LOGOUT_RESPONSE: %w", err)
	}
	return LogoutResponse{Reason: reason, Instant: instant}, nil
}
