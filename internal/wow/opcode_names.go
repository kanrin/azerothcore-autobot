package wow

import "fmt"

// opcodeNames maps the opcodes declared in opcodes.go to their AzerothCore
// names so that logs, telemetry and error messages stay readable. Opcodes this
// project does not handle fall back to a hex rendering.
var opcodeNames = map[uint16]string{
	CmsgQuestQuery:         "CMSG_QUEST_QUERY",
	SmsgQuestQueryResponse: "SMSG_QUEST_QUERY_RESPONSE",

	CmsgGossipHello:        "CMSG_GOSSIP_HELLO",
	CmsgGossipSelectOption: "CMSG_GOSSIP_SELECT_OPTION",
	SmsgGossipMessage:      "SMSG_GOSSIP_MESSAGE",
	SmsgGossipComplete:     "SMSG_GOSSIP_COMPLETE",
	CmsgNpcTextQuery:       "CMSG_NPC_TEXT_QUERY",
	SmsgNpcTextUpdate:      "SMSG_NPC_TEXT_UPDATE",

	SmsgQuestgiverStatus:        "SMSG_QUESTGIVER_STATUS",
	CmsgQuestgiverHello:         "CMSG_QUESTGIVER_HELLO",
	SmsgQuestgiverQuestList:     "SMSG_QUESTGIVER_QUEST_LIST",
	CmsgQuestgiverQueryQuest:    "CMSG_QUESTGIVER_QUERY_QUEST",
	SmsgQuestgiverQuestDetails:  "SMSG_QUESTGIVER_QUEST_DETAILS",
	CmsgQuestgiverAcceptQuest:   "CMSG_QUESTGIVER_ACCEPT_QUEST",
	CmsgQuestgiverCompleteQuest: "CMSG_QUESTGIVER_COMPLETE_QUEST",
	SmsgQuestgiverRequestItems:  "SMSG_QUESTGIVER_REQUEST_ITEMS",
	SmsgQuestgiverOfferReward:   "SMSG_QUESTGIVER_OFFER_REWARD",
	CmsgQuestgiverChooseReward:  "CMSG_QUESTGIVER_CHOOSE_REWARD",
	SmsgQuestgiverQuestComplete: "SMSG_QUESTGIVER_QUEST_COMPLETE",

	SmsgQuestupdateComplete: "SMSG_QUESTUPDATE_COMPLETE",
	SmsgQuestupdateAddKill:  "SMSG_QUESTUPDATE_ADD_KILL",
	SmsgQuestupdateAddItem:  "SMSG_QUESTUPDATE_ADD_ITEM",

	CmsgQuestgiverStatusMultipleQuery: "CMSG_QUESTGIVER_STATUS_MULTIPLE_QUERY",
	SmsgQuestgiverStatusMultiple:      "SMSG_QUESTGIVER_STATUS_MULTIPLE",

	CmsgLfgGetStatus:          "CMSG_LFG_GET_STATUS",
	CmsgLfgJoin:               "CMSG_LFG_JOIN",
	CmsgLfgLeave:              "CMSG_LFG_LEAVE",
	CmsgSetLfgComment:         "CMSG_SET_LFG_COMMENT",
	CmsgLfgProposalResult:     "CMSG_LFG_PROPOSAL_RESULT",
	CmsgLfgSetRoles:           "CMSG_LFG_SET_ROLES",
	CmsgLfgSetBootVote:        "CMSG_LFG_SET_BOOT_VOTE",
	CmsgLfgTeleport:           "CMSG_LFG_TELEPORT",
	SmsgLfgPlayerReward:       "SMSG_LFG_PLAYER_REWARD",
	SmsgLfgTeleportDenied:     "SMSG_LFG_TELEPORT_DENIED",
	SmsgLfgOfferContinue:      "SMSG_LFG_OFFER_CONTINUE",
	SmsgLfgRoleChosen:         "SMSG_LFG_ROLE_CHOSEN",
	SmsgLfgProposalUpdate:     "SMSG_LFG_PROPOSAL_UPDATE",
	SmsgLfgRoleCheckUpdate:    "SMSG_LFG_ROLE_CHECK_UPDATE",
	SmsgLfgJoinResult:         "SMSG_LFG_JOIN_RESULT",
	SmsgLfgQueueStatus:        "SMSG_LFG_QUEUE_STATUS",
	SmsgLfgUpdatePlayer:       "SMSG_LFG_UPDATE_PLAYER",
	SmsgLfgUpdateParty:        "SMSG_LFG_UPDATE_PARTY",
	SmsgLfgUpdateSearch:       "SMSG_LFG_UPDATE_SEARCH",
	SmsgLfgBootProposalUpdate: "SMSG_LFG_BOOT_PROPOSAL_UPDATE",
	SmsgLfgPlayerInfo:         "SMSG_LFG_PLAYER_INFO",
	SmsgLfgPartyInfo:          "SMSG_LFG_PARTY_INFO",
	SmsgLfgDisabled:           "SMSG_LFG_DISABLED",

	CmsgCharDelete: "CMSG_CHAR_DELETE",
	SmsgCharDelete: "SMSG_CHAR_DELETE",
}

// OpcodeName returns the AzerothCore name of opcode, or a hex rendering such as
// "0x1234" when the opcode is not one this project handles.
func OpcodeName(opcode uint16) string {
	if name, ok := opcodeNames[opcode]; ok {
		return name
	}
	return fmt.Sprintf("0x%04X", opcode)
}
