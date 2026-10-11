package packet

// Dungeon finder (LFG) client packet encoders.
//
// AzerothGhost v1.0.9 ships no LFG opcodes and no LFG packets at all, so every
// encoder here is new work. Layouts come from
// src/server/game/Server/Packets/LFGPackets.cpp and the handlers in
// src/server/game/Handlers/LFGHandler.cpp.

// LfgNeedsCount is the number of role requirement bytes in CMSG_LFG_JOIN.
//
// AC: LFGPackets.cpp:32 reads and discards a count byte
// (`read_skip<uint8>(); // Needs count, hardcoded to 3 in client`) before three
// uint8 values. The count itself is ignored, but its byte must still be present
// or the trailing Comment is decoded from the wrong offset.
const LfgNeedsCount = 3

// EncodeLfgJoin encodes CMSG_LFG_JOIN (0x35C).
//
// AC: LFGPackets.cpp:20-34, LFGJoin::Read:
//
//	_worldPacket >> Roles;                              // uint32
//	_worldPacket >> NoPartialClear;                     // bool -> 1 byte
//	_worldPacket >> Achievements;                       // bool -> 1 byte
//	Slots.resize(_worldPacket.read<uint8>());           // uint8 count
//	for (uint32& slot : Slots) _worldPacket >> slot;    // uint32 each
//	_worldPacket.read_skip<uint8>();                    // needs count, always 3
//	for (uint8& needs : Needs) _worldPacket >> needs;   // 3 x uint8
//	_worldPacket >> Comment;                            // CString, not length-prefixed
//
// Roles is a uint32 on the wire but the server narrows it with
// uint8(packet.Roles), so only the low byte carries meaning. Use the LfgRole
// bits from the wow package.
//
// slots entries must be packed with wow.LfgSlot — dungeon id in the low 24 bits
// and the dungeon type in the high byte. At most 255 slots can be represented;
// extra entries are silently dropped by the uint8 count.
//
// Two server-side silent rejections are worth remembering (LFGHandler.cpp:50-61):
// an empty slot list is discarded, and the request is ignored unless the player
// is the group leader (or solo).
func EncodeLfgJoin(roles uint8, slots []uint32, needs [LfgNeedsCount]uint8, comment string) []byte {
	w := NewWriter().
		Uint32(uint32(roles)).
		Bool(false). // NoPartialClear
		Bool(false). // Achievements
		Uint8(uint8(len(slots)))
	for _, slot := range slots {
		w.Uint32(slot)
	}
	w.Uint8(LfgNeedsCount)
	for _, need := range needs {
		w.Uint8(need)
	}
	w.CString(comment)
	return w.Bytes()
}

// EncodeLfgLeave encodes CMSG_LFG_LEAVE (0x35D).
//
// AC: LFGHandler.cpp:78-80, registered at Opcodes.cpp:992. The handler ignores
// the body entirely, so the payload is empty.
func EncodeLfgLeave() []byte {
	return NewWriter().Bytes()
}

// EncodeLfgProposalResult encodes CMSG_LFG_PROPOSAL_RESULT (0x362).
//
// AC: LFGHandler.cpp:95-104 — `recvData >> proposalID >> accept`, i.e. a uint32
// followed by one byte for the bool. proposalID is the "LFG group id" echoed in
// SMSG_LFG_PROPOSAL_UPDATE.
func EncodeLfgProposalResult(proposalID uint32, accept bool) []byte {
	return NewWriter().Uint32(proposalID).Bool(accept).Bytes()
}

// EncodeLfgSetRoles encodes CMSG_LFG_SET_ROLES (0x36A).
//
// AC: LFGHandler.cpp:106-120 — a single uint8 role mask, answered only when the
// player is in a group (otherwise the packet is dropped with just a debug log).
func EncodeLfgSetRoles(roles uint8) []byte {
	return NewWriter().Uint8(roles).Bytes()
}

// EncodeLfgSetBootVote encodes CMSG_LFG_SET_BOOT_VOTE (0x36C).
//
// AC: LFGHandler.cpp:133-141 — `recvData >> agree` (one byte).
func EncodeLfgSetBootVote(agree bool) []byte {
	return NewWriter().Bool(agree).Bytes()
}

// EncodeSetLfgComment encodes CMSG_SET_LFG_COMMENT (0x366).
//
// AC: LFGHandler.cpp:122-131, registered at Opcodes.cpp:1001 — the handler is
// `HandleLfgSetCommentOpcode` and reads `recvData >> comment`, a CString.
//
// The opcode number is 0x366, not 0x35E: 0x35E is CMSG_SEARCH_LFG_JOIN (raid
// browser), a different feature.
func EncodeSetLfgComment(comment string) []byte {
	return NewWriter().CString(comment).Bytes()
}

// EncodeLfgGetStatus encodes CMSG_LFG_GET_STATUS (0x296).
//
// AC: LFGHandler.cpp:281-291 — the handler takes the packet but reads nothing,
// so the payload is empty.
func EncodeLfgGetStatus() []byte {
	return NewWriter().Bytes()
}

// EncodeLfgTeleport encodes CMSG_LFG_TELEPORT (0x370).
//
// AC: LFGHandler.cpp:143-150 — `recvData >> out` (one byte). out=true teleports
// into the dungeon, out=false teleports out of it.
func EncodeLfgTeleport(out bool) []byte {
	return NewWriter().Bool(out).Bytes()
}
