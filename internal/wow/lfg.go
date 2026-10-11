package wow

import (
	"context"
	"errors"
	"time"

	"azerothcore-autobot/internal/wow/packet"
)

// LfgJoinRequest is everything CMSG_LFG_JOIN carries.
//
// AzerothGhost v1.0.9 has no LFG support at all, so this is the project's own
// client-side implementation of the queue handshake.
type LfgJoinRequest struct {
	// Roles is the role the bot will play. Exactly one of tank/healer/damage is
	// what both the 3.3.5a client and AzerothCore expect.
	Roles LfgRole

	// DungeonSlots lists the content to queue for, packed with LfgSlot. It must
	// not be empty: AC drops a join whose slots do not resolve to a known dungeon
	// without sending a reply, so the caller sees silence instead of an error.
	DungeonSlots []uint32

	// Comment is the player comment shown to the rest of the group.
	Comment string
}

// LfgJoin sends CMSG_LFG_JOIN.
func (s *Session) LfgJoin(req LfgJoinRequest) error {
	if len(req.DungeonSlots) == 0 {
		return errors.New("wow: LFG join needs at least one dungeon slot; AC silently ignores an empty list")
	}
	payload := packet.EncodeLfgJoin(uint8(req.Roles), req.DungeonSlots, LfgDefaultNeeds, req.Comment)
	return s.Send(CmsgLfgJoin, payload)
}

// LfgLeave sends CMSG_LFG_LEAVE, which removes the bot from the queue or the
// dungeon group and rescues it from a stuck state.
//
// Use it on every failure path: leaving a group by disconnecting instead applies
// the Dungeon Deserter debuff (spell 71041) and blocks the next queue.
func (s *Session) LfgLeave() error {
	return s.Send(CmsgLfgLeave, packet.EncodeLfgLeave())
}

// LfgSetRoles sends CMSG_LFG_SET_ROLES to change the role the bot offers while
// queued.
func (s *Session) LfgSetRoles(roles LfgRole) error {
	return s.Send(CmsgLfgSetRoles, packet.EncodeLfgSetRoles(uint8(roles)))
}

// LfgProposalResult sends CMSG_LFG_PROPOSAL_RESULT, the accept/decline answer to
// SMSG_LFG_PROPOSAL_UPDATE.
//
// The proposal id must be the one from that packet: AC looks it up and ignores
// the answer if it does not match the pending proposal.
func (s *Session) LfgProposalResult(proposalID uint32, accept bool) error {
	return s.Send(CmsgLfgProposalResult, packet.EncodeLfgProposalResult(proposalID, accept))
}

// LfgSetBootVote sends CMSG_LFG_SET_BOOT_VOTE, agreeing or disagreeing with a
// vote to remove a group member (SMSG_LFG_BOOT_PROPOSAL_UPDATE).
func (s *Session) LfgSetBootVote(agree bool) error {
	return s.Send(CmsgLfgSetBootVote, packet.EncodeLfgSetBootVote(agree))
}

// LfgTeleport tells the server whether the bot accepts the teleport into (true)
// or out of (false) the dungeon group.
func (s *Session) LfgTeleport(out bool) error {
	return s.Send(CmsgLfgTeleport, packet.EncodeLfgTeleport(out))
}

// LfgGetStatus sends CMSG_LFG_GET_STATUS, a cheap way to resynchronise the queue
// view after a reconnect: the answer is SMSG_LFG_QUEUE_STATUS plus the update
// pair.
func (s *Session) LfgGetStatus() error {
	return s.Send(CmsgLfgGetStatus, packet.EncodeLfgGetStatus())
}

// SetLfgComment sends CMSG_SET_LFG_COMMENT (opcode 0x366).
//
// The opcode is easy to get wrong by pattern-matching the LFG block: 0x35E is
// CMSG_SEARCH_LFG_JOIN, an unrelated raid-browser packet.
func (s *Session) SetLfgComment(comment string) error {
	return s.Send(CmsgSetLfgComment, packet.EncodeSetLfgComment(comment))
}

// JoinLfgQueue sends CMSG_LFG_JOIN and waits for the server's verdict.
//
// Only SMSG_LFG_JOIN_RESULT is awaited here, not the SMSG_LFG_UPDATE_PLAYER /
// SMSG_LFG_UPDATE_PARTY that follows it: which of those arrives depends on
// whether the bot ended up as party leader, and the LFG feature needs to keep
// listening for both anyway. Use Arm once and consume from the exchange for the
// whole queue lifetime instead of calling this in a loop.
func (s *Session) JoinLfgQueue(ctx context.Context, req LfgJoinRequest, timeout time.Duration) ([]byte, error) {
	if len(req.DungeonSlots) == 0 {
		return nil, errors.New("wow: LFG join needs at least one dungeon slot; AC silently ignores an empty list")
	}

	payload := packet.EncodeLfgJoin(uint8(req.Roles), req.DungeonSlots, LfgDefaultNeeds, req.Comment)
	_, data, err := s.Do(ctx, CmsgLfgJoin, payload, []uint16{SmsgLfgJoinResult, SmsgLfgDisabled}, timeout)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// LfgLifetimeReplies lists every packet the LFG feature must watch for once it
// is queued, so it can arm one exchange for the whole queue lifetime rather than
// arming per packet and missing a fast reply.
//
// Covers: role check, queue status, the proposal, both update variants, the boot
// vote, the continue offer, and the server turning the feature off.
func LfgLifetimeReplies() []uint16 {
	return []uint16{
		SmsgLfgJoinResult,
		SmsgLfgRoleCheckUpdate,
		SmsgLfgQueueStatus,
		SmsgLfgProposalUpdate,
		SmsgLfgUpdatePlayer,
		SmsgLfgUpdateParty,
		SmsgLfgUpdateSearch,
		SmsgLfgBootProposalUpdate,
		SmsgLfgOfferContinue,
		SmsgLfgTeleportDenied,
		SmsgLfgDisabled,
	}
}
