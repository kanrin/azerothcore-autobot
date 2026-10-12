// Package wow is the protocol layer of the autobot.
//
// It wraps the AzerothGhost client with a single-writer command surface, a
// packet codec and the opcode numbers this project needs, and it caches nothing
// of its own beyond what the caller asks for. It contains no business logic:
// deciding *what* to do belongs to internal/agent and internal/feature.
//
// Dependency direction: internal/wow may import internal/wow/packet and the
// AzerothGhost client; nothing else in this module may reach past it to
// AzerothGhost directly.
package wow

// World of Warcraft 3.3.5a opcode numbers, copied verbatim from AzerothCore:
//
//	azerothcore-wotlk/src/server/game/Server/Protocol/Opcodes.h
//	azerothcore-wotlk/src/server/game/Server/Protocol/Opcodes.cpp  (registration + line numbers)
//
// Do NOT take opcode numbers from AzerothGhost v1.0.9. Its constant for the
// "quest objective completed" notification is wrong:
//
//	AzerothGhost  client/world.go:162    SmsgQuestupdateComplete = 0x01A8  // wrong
//	AzerothCore   Opcodes.h              SMSG_QUESTUPDATE_COMPLETE = 0x198  // correct
//
// A wrong opcode fails silently: the packet simply never matches and quest
// progress looks frozen. TestOpcodeQuestUpdateComplete pins the correct value
// so the mistake cannot come back.
//
// AzerothGhost also has no LFG opcode at all, hence the whole LFG block below.
const (
	// Quest definition lookup.
	CmsgQuestQuery         uint16 = 0x05C
	SmsgQuestQueryResponse uint16 = 0x05D

	// Gossip and NPC text.
	CmsgGossipHello        uint16 = 0x17B
	CmsgGossipSelectOption uint16 = 0x17C
	SmsgGossipMessage      uint16 = 0x17D
	SmsgGossipComplete     uint16 = 0x17E
	CmsgNpcTextQuery       uint16 = 0x17F
	SmsgNpcTextUpdate      uint16 = 0x180

	// Questgiver interaction.
	SmsgQuestgiverStatus        uint16 = 0x183
	CmsgQuestgiverHello         uint16 = 0x184
	SmsgQuestgiverQuestList     uint16 = 0x185
	CmsgQuestgiverQueryQuest    uint16 = 0x186
	SmsgQuestgiverQuestDetails  uint16 = 0x188
	CmsgQuestgiverAcceptQuest   uint16 = 0x189
	CmsgQuestgiverCompleteQuest uint16 = 0x18A
	SmsgQuestgiverRequestItems  uint16 = 0x18B
	SmsgQuestgiverOfferReward   uint16 = 0x18D
	CmsgQuestgiverChooseReward  uint16 = 0x18E
	SmsgQuestgiverQuestInvalid  uint16 = 0x18F
	SmsgQuestgiverQuestComplete uint16 = 0x191

	// Quest log updates pushed by the server.
	SmsgQuestupdateFailed      uint16 = 0x196
	SmsgQuestupdateFailedTimer uint16 = 0x197
	SmsgQuestupdateComplete    uint16 = 0x198
	SmsgQuestupdateAddKill     uint16 = 0x199
	SmsgQuestupdateAddItem     uint16 = 0x19A

	// Batch questgiver status query/response.
	CmsgQuestgiverStatusMultipleQuery uint16 = 0x417
	SmsgQuestgiverStatusMultiple      uint16 = 0x418

	// Dungeon finder (LFG). Entirely absent from AzerothGhost v1.0.9.
	CmsgLfgGetStatus          uint16 = 0x296
	CmsgLfgJoin               uint16 = 0x35C
	CmsgLfgLeave              uint16 = 0x35D
	CmsgSetLfgComment         uint16 = 0x366
	CmsgLfgProposalResult     uint16 = 0x362
	CmsgLfgSetRoles           uint16 = 0x36A
	CmsgLfgSetBootVote        uint16 = 0x36C
	CmsgLfgTeleport           uint16 = 0x370
	SmsgLfgPlayerReward       uint16 = 0x1FF
	SmsgLfgTeleportDenied     uint16 = 0x200
	SmsgLfgOfferContinue      uint16 = 0x293
	SmsgLfgRoleChosen         uint16 = 0x2BB
	SmsgLfgProposalUpdate     uint16 = 0x361
	SmsgLfgRoleCheckUpdate    uint16 = 0x363
	SmsgLfgJoinResult         uint16 = 0x364
	SmsgLfgQueueStatus        uint16 = 0x365
	SmsgLfgUpdatePlayer       uint16 = 0x367
	SmsgLfgUpdateParty        uint16 = 0x368
	SmsgLfgUpdateSearch       uint16 = 0x369
	SmsgLfgBootProposalUpdate uint16 = 0x36D
	SmsgLfgPlayerInfo         uint16 = 0x36F
	SmsgLfgPartyInfo          uint16 = 0x372
	SmsgLfgDisabled           uint16 = 0x398

	// Character management.
	//
	// Source: azerothcore-wotlk/src/server/game/Server/Protocol/Opcodes.h:86,90.
	// CMSG_CHAR_DELETE is a raw little-endian uint64 GUID, not a packed one
	// (CharacterHandler.cpp:614-617 reads it through ObjectGuid.cpp:76-80, which
	// is a plain uint64 read). AzerothGhost v1.0.9 declares SmsgCharDelete but
	// never parses it, so the reply has to be armed through Session.Do.
	CmsgCharDelete uint16 = 0x038
	SmsgCharDelete uint16 = 0x03C

	// Logout handshake.
	//
	// Source: Opcodes.h:105-107. CMSG_LOGOUT_REQUEST has an empty body; the
	// server always answers SMSG_LOGOUT_RESPONSE (uint32 reason, uint8 instant)
	// and, unless it refuses the logout, sends SMSG_LOGOUT_COMPLETE later — 20
	// seconds later when the logout is not instant (WorldSession.h:547). See
	// Session.Logout, which arms both replies before sending.
	CmsgLogoutRequest  uint16 = 0x04B
	SmsgLogoutResponse uint16 = 0x04C
	SmsgLogoutComplete uint16 = 0x04D
)
