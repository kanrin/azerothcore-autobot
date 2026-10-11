package wow

import "testing"

// TestOpcodeQuestUpdateComplete pins the one value AzerothGhost v1.0.9 gets
// wrong.
//
// AzerothGhost client/world.go:162 declares
//
//	SmsgQuestupdateComplete uint16 = 0x01A8
//
// but AzerothCore defines SMSG_QUESTUPDATE_COMPLETE = 0x198 (Opcodes.h, and the
// handler table in Opcodes.cpp registers the server handler at index 0x198).
// Using the upstream constant means the "objective complete" notification never
// matches, which presents as a quest log that appears frozen rather than as a
// protocol error — the most expensive kind of bug to find later.
func TestOpcodeQuestUpdateComplete(t *testing.T) {
	const want uint16 = 0x198

	if SmsgQuestupdateComplete != want {
		t.Fatalf("SmsgQuestupdateComplete = 0x%03X, want 0x%03X (SMSG_QUESTUPDATE_COMPLETE per AC Opcodes.h)",
			SmsgQuestupdateComplete, want)
	}
	if SmsgQuestupdateComplete == 0x01A8 {
		t.Fatal("opcode regressed to the wrong AzerothGhost value 0x01A8")
	}
}

// TestOpcodeValues records the client opcode numbers this project sends, so an
// accidental edit is caught here rather than on a live server. Values are from
// azerothcore-wotlk/src/server/game/Server/Protocol/Opcodes.h; the comment on
// each entry cites where the handler is registered in Opcodes.cpp.
func TestOpcodeValues(t *testing.T) {
	tests := []struct {
		name string
		got  uint16
		want uint16
	}{
		// Quest definition and gossip.
		{"CMSG_QUEST_QUERY", CmsgQuestQuery, 0x05C},
		{"SMSG_QUEST_QUERY_RESPONSE", SmsgQuestQueryResponse, 0x05D},
		{"CMSG_GOSSIP_HELLO", CmsgGossipHello, 0x17B},
		{"CMSG_GOSSIP_SELECT_OPTION", CmsgGossipSelectOption, 0x17C},
		{"SMSG_GOSSIP_MESSAGE", SmsgGossipMessage, 0x17D},
		{"SMSG_GOSSIP_COMPLETE", SmsgGossipComplete, 0x17E},
		{"CMSG_NPC_TEXT_QUERY", CmsgNpcTextQuery, 0x17F},
		{"SMSG_NPC_TEXT_UPDATE", SmsgNpcTextUpdate, 0x180},

		// Questgiver interaction.
		{"SMSG_QUESTGIVER_STATUS", SmsgQuestgiverStatus, 0x183},
		{"CMSG_QUESTGIVER_HELLO", CmsgQuestgiverHello, 0x184},
		{"SMSG_QUESTGIVER_QUEST_LIST", SmsgQuestgiverQuestList, 0x185},
		{"CMSG_QUESTGIVER_QUERY_QUEST", CmsgQuestgiverQueryQuest, 0x186},
		{"SMSG_QUESTGIVER_QUEST_DETAILS", SmsgQuestgiverQuestDetails, 0x188},
		{"CMSG_QUESTGIVER_ACCEPT_QUEST", CmsgQuestgiverAcceptQuest, 0x189},
		{"CMSG_QUESTGIVER_COMPLETE_QUEST", CmsgQuestgiverCompleteQuest, 0x18A},
		{"SMSG_QUESTGIVER_REQUEST_ITEMS", SmsgQuestgiverRequestItems, 0x18B},
		{"SMSG_QUESTGIVER_OFFER_REWARD", SmsgQuestgiverOfferReward, 0x18D},
		{"CMSG_QUESTGIVER_CHOOSE_REWARD", CmsgQuestgiverChooseReward, 0x18E},
		{"SMSG_QUESTGIVER_QUEST_COMPLETE", SmsgQuestgiverQuestComplete, 0x191},

		// Quest log updates.
		{"SMSG_QUESTUPDATE_COMPLETE", SmsgQuestupdateComplete, 0x198},
		{"SMSG_QUESTUPDATE_ADD_KILL", SmsgQuestupdateAddKill, 0x199},
		{"SMSG_QUESTUPDATE_ADD_ITEM", SmsgQuestupdateAddItem, 0x19A},

		{"CMSG_QUESTGIVER_STATUS_MULTIPLE_QUERY", CmsgQuestgiverStatusMultipleQuery, 0x417},
		{"SMSG_QUESTGIVER_STATUS_MULTIPLE", SmsgQuestgiverStatusMultiple, 0x418},

		// LFG: none of these exist upstream in AzerothGhost v1.0.9.
		{"CMSG_LFG_GET_STATUS", CmsgLfgGetStatus, 0x296},
		{"CMSG_LFG_JOIN", CmsgLfgJoin, 0x35C},
		{"CMSG_LFG_LEAVE", CmsgLfgLeave, 0x35D},
		{"CMSG_LFG_PROPOSAL_RESULT", CmsgLfgProposalResult, 0x362},
		{"CMSG_SET_LFG_COMMENT", CmsgSetLfgComment, 0x366},
		{"CMSG_LFG_SET_ROLES", CmsgLfgSetRoles, 0x36A},
		{"CMSG_LFG_SET_BOOT_VOTE", CmsgLfgSetBootVote, 0x36C},
		{"CMSG_LFG_TELEPORT", CmsgLfgTeleport, 0x370},
		{"SMSG_LFG_PLAYER_REWARD", SmsgLfgPlayerReward, 0x1FF},
		{"SMSG_LFG_TELEPORT_DENIED", SmsgLfgTeleportDenied, 0x200},
		{"SMSG_LFG_OFFER_CONTINUE", SmsgLfgOfferContinue, 0x293},
		{"SMSG_LFG_ROLE_CHOSEN", SmsgLfgRoleChosen, 0x2BB},
		{"SMSG_LFG_PROPOSAL_UPDATE", SmsgLfgProposalUpdate, 0x361},
		{"SMSG_LFG_ROLE_CHECK_UPDATE", SmsgLfgRoleCheckUpdate, 0x363},
		{"SMSG_LFG_JOIN_RESULT", SmsgLfgJoinResult, 0x364},
		{"SMSG_LFG_QUEUE_STATUS", SmsgLfgQueueStatus, 0x365},
		{"SMSG_LFG_UPDATE_PLAYER", SmsgLfgUpdatePlayer, 0x367},
		{"SMSG_LFG_UPDATE_PARTY", SmsgLfgUpdateParty, 0x368},
		{"SMSG_LFG_UPDATE_SEARCH", SmsgLfgUpdateSearch, 0x369},
		{"SMSG_LFG_BOOT_PROPOSAL_UPDATE", SmsgLfgBootProposalUpdate, 0x36D},
		{"SMSG_LFG_PLAYER_INFO", SmsgLfgPlayerInfo, 0x36F},
		{"SMSG_LFG_PARTY_INFO", SmsgLfgPartyInfo, 0x372},
		{"SMSG_LFG_DISABLED", SmsgLfgDisabled, 0x398},

		// Character management: the delete path added so provisioning has a
		// cleanup. Both values are AC Opcodes.h:86 and 90.
		{"CMSG_CHAR_DELETE", CmsgCharDelete, 0x038},
		{"SMSG_CHAR_DELETE", SmsgCharDelete, 0x03C},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("%s = 0x%03X, want 0x%03X", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestCmsgSetLfgCommentIsNotSearchLfgJoin guards a near-miss found while
// writing the packet code: the comment opcode is 0x366, while 0x35E — the value
// one might guess by pattern-matching the LFG block — is CMSG_SEARCH_LFG_JOIN,
// an unrelated raid-browser packet.
func TestCmsgSetLfgCommentIsNotSearchLfgJoin(t *testing.T) {
	const searchLfgJoin uint16 = 0x35E
	if CmsgSetLfgComment == searchLfgJoin {
		t.Fatalf("CmsgSetLfgComment = 0x%03X, which is CMSG_SEARCH_LFG_JOIN", searchLfgJoin)
	}
	if CmsgSetLfgComment != 0x366 {
		t.Fatalf("CmsgSetLfgComment = 0x%03X, want 0x366", CmsgSetLfgComment)
	}
}

// TestOpcodeName covers a known opcode and the hex fallback used for the many
// packets this project does not model.
func TestOpcodeName(t *testing.T) {
	if got := OpcodeName(CmsgLfgJoin); got != "CMSG_LFG_JOIN" {
		t.Errorf("OpcodeName(CmsgLfgJoin) = %q, want %q", got, "CMSG_LFG_JOIN")
	}
	if got := OpcodeName(SmsgQuestupdateComplete); got != "SMSG_QUESTUPDATE_COMPLETE" {
		t.Errorf("OpcodeName(SmsgQuestupdateComplete) = %q, want %q", got, "SMSG_QUESTUPDATE_COMPLETE")
	}
	if got := OpcodeName(0x0ABC); got != "0x0ABC" {
		t.Errorf("OpcodeName(0x0ABC) = %q, want %q", got, "0x0ABC")
	}
}
