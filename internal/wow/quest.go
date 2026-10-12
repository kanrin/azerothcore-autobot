package wow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"azerothcore-autobot/internal/wow/packet"
	"azerothcore-autobot/internal/wow/state"
)

// The questgiver conversation: what a bot sends to take a quest, and what confirms
// each step.
//
//	QuestGiverHello  CMSG_QUESTGIVER_HELLO        → SMSG_QUESTGIVER_QUEST_LIST
//	QueryQuest       CMSG_QUESTGIVER_QUERY_QUEST  → SMSG_QUESTGIVER_QUEST_DETAILS
//	                                                 (or SMSG_QUESTGIVER_QUEST_INVALID)
//	AcceptQuest      CMSG_QUESTGIVER_ACCEPT_QUEST → the quest appearing in the log
//
// The third one is the interesting case. AzerothCore answers an accept with
// SMSG_GOSSIP_COMPLETE whether it worked or not (QuestHandler.cpp:166-197 — the
// handler calls SendCloseGossip on the way out of both paths), so there is nothing
// to wait *for*: the confirmation is the player's own quest log changing, which is an
// update-field change rather than a packet. AcceptQuest therefore waits on the
// object cache, which is also exactly what a client displays.
//
// The rest of this file is still send-only, and says so: turn-in needs the same
// waiting treatment (SMSG_QUESTGIVER_REQUEST_ITEMS / OFFER_REWARD), so an API that
// only sends will not survive the next slice. Every payload comes from
// internal/wow/packet, which carries the golden-byte tests transcribed from
// AzerothCore — never hand-roll bytes here.

// DefaultQuestTimeout bounds one questgiver round trip. The server answers these
// from memory, so a timeout means the request never got through: wrong NPC, out of
// range, or a phase the bot is not in.
const DefaultQuestTimeout = 15 * time.Second

// QuestFailedReason is the reason code of SMSG_QUESTGIVER_QUEST_INVALID.
//
// Values are AzerothCore's QuestFailedReason (QuestDef.h:48-60). Only the ones a bot
// can act on are named; anything else renders as a number, because inventing a name
// for a code we have not seen would be worse than showing it.
type QuestFailedReason uint32

// Refusal reasons a bot is likely to meet.
const (
	QuestFailedNoRequirements QuestFailedReason = 0  // INVALIDREASON_DONT_HAVE_REQ
	QuestFailedLowLevel       QuestFailedReason = 1  // INVALIDREASON_QUEST_FAILED_LOW_LEVEL
	QuestFailedWrongRace      QuestFailedReason = 6  // INVALIDREASON_QUEST_FAILED_WRONG_RACE
	QuestFailedAlreadyDone    QuestFailedReason = 7  // INVALIDREASON_QUEST_ALREADY_DONE
	QuestFailedAlreadyOn      QuestFailedReason = 13 // INVALIDREASON_QUEST_ALREADY_ON
	QuestFailedMissingItems   QuestFailedReason = 21 // INVALIDREASON_QUEST_FAILED_MISSING_ITEMS
	QuestFailedNotEnoughMoney QuestFailedReason = 23 // INVALIDREASON_QUEST_FAILED_NOT_ENOUGH_MONEY
)

// String names the reason for logs.
func (r QuestFailedReason) String() string {
	switch r {
	case QuestFailedNoRequirements:
		return "does not have the requirements"
	case QuestFailedLowLevel:
		return "level too low"
	case QuestFailedWrongRace:
		return "wrong race"
	case QuestFailedAlreadyDone:
		return "already done"
	case QuestFailedAlreadyOn:
		return "already on the quest"
	case QuestFailedMissingItems:
		return "missing required items"
	case QuestFailedNotEnoughMoney:
		return "not enough money"
	default:
		return fmt.Sprintf("reason %d", uint32(r))
	}
}

// QuestGiverHello opens the quest dialog for one NPC and returns the quest list it
// offers.
//
// AC QuestHandler.cpp:79-82 reads the guid; HandleQuestgiverHelloOpcode answers with
// the list. A creature that offers nothing still answers, with an empty list, which is
// why the caller gets a list rather than an error.
func (s *Session) QuestGiverHello(ctx context.Context, npcGUID uint64, timeout time.Duration) (packet.QuestList, error) {
	var empty packet.QuestList
	if npcGUID == 0 {
		return empty, errors.New("wow: questgiver hello: guid is zero")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverHello,
		packet.EncodeQuestgiverHello(npcGUID), []uint16{SmsgQuestgiverQuestList}, timeout)
	if err != nil {
		return empty, fmt.Errorf("wow: questgiver hello: %w", err)
	}
	if opcode != SmsgQuestgiverQuestList {
		return empty, fmt.Errorf("wow: questgiver hello: unexpected reply %s", OpcodeName(opcode))
	}

	list, err := packet.ParseQuestGiverQuestList(data)
	if err != nil {
		return empty, err
	}
	return list, nil
}

// QueryQuest asks for one quest's details.
//
// Two refusals are reported with their cause: SMSG_QUESTGIVER_QUEST_INVALID carries
// the reason, and SMSG_GOSSIP_COMPLETE means the server dropped the dialog — the usual
// cause is asking an NPC about a quest it neither offers nor is involved in
// (QuestHandler.cpp:203-215 returns silently in that case).
func (s *Session) QueryQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (packet.QuestDetails, error) {
	var empty packet.QuestDetails
	if npcGUID == 0 || questID == 0 {
		return empty, errors.New("wow: query quest: guid and quest id are required")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverQueryQuest,
		packet.EncodeQuestgiverQueryQuest(npcGUID, questID, 0),
		[]uint16{SmsgQuestgiverQuestDetails, SmsgQuestgiverQuestInvalid, SmsgGossipComplete}, timeout)
	if err != nil {
		return empty, fmt.Errorf("wow: query quest %d: %w", questID, err)
	}

	switch opcode {
	case SmsgQuestgiverQuestDetails:
		return packet.ParseQuestGiverQuestDetails(data)

	case SmsgQuestgiverQuestInvalid:
		invalid, parseErr := packet.ParseQuestGiverQuestInvalid(data)
		if parseErr != nil {
			return empty, parseErr
		}
		return empty, fmt.Errorf("wow: query quest %d refused: %s",
			questID, QuestFailedReason(invalid.Reason))

	case SmsgGossipComplete:
		return empty, fmt.Errorf("wow: query quest %d: %s closed the dialog "+
			"(the NPC neither offers that quest nor is involved in it)",
			questID, OpcodeName(opcode))

	default:
		return empty, fmt.Errorf("wow: query quest %d: unexpected reply %s", questID, OpcodeName(opcode))
	}
}

// AcceptQuest accepts a quest and waits until it is in the bot's quest log.
//
// The wait is the point: the server closes the gossip dialog on success and on refusal
// alike, so the log is the only thing that distinguishes them. A timeout therefore
// means "the server did not add it", and the caller is expected to say why in its own
// terms — the requirements are server-side data (quest_template), which this layer
// deliberately knows nothing about.
func (s *Session) AcceptQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) error {
	if npcGUID == 0 || questID == 0 {
		return errors.New("wow: accept quest: guid and quest id are required")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	if err := s.Send(CmsgQuestgiverAcceptQuest,
		packet.EncodeQuestgiverAcceptQuest(npcGUID, questID)); err != nil {
		return fmt.Errorf("wow: accept quest %d: %w", questID, err)
	}

	err := s.WaitFor(ctx, timeout, fmt.Sprintf("quest %d to appear in the quest log", questID),
		func() bool { return state.Read(s).HasQuest(questID) })
	if err != nil {
		return fmt.Errorf("wow: accept quest %d: %w", questID, err)
	}
	return nil
}

// QuestLog returns the bot's current quest log.
//
// It is a convenience for callers that would otherwise import internal/wow/state just
// to ask "which quests am I on".
func (s *Session) QuestLog() []state.QuestSlot { return state.Read(s).Quests }

// QueryQuestDefinition sends CMSG_QUEST_QUERY for the static definition of a quest
// (level, objectives, rewards) — the data a bot wants before deciding whether a quest
// is worth taking. Send-only: the answer is SMSG_QUEST_QUERY_RESPONSE, which nothing
// parses yet.
func (s *Session) QueryQuestDefinition(questID uint32) error {
	return s.Send(CmsgQuestQuery, packet.EncodeQuestQuery(questID))
}

// CompleteQuest sends CMSG_QUESTGIVER_COMPLETE_QUEST, which asks the giver to
// validate the finished objectives. Depending on the quest the server answers
// with SMSG_QUESTGIVER_REQUEST_ITEMS or SMSG_QUESTGIVER_OFFER_REWARD.
func (s *Session) CompleteQuest(npcGUID uint64, questID uint32) error {
	return s.Send(CmsgQuestgiverCompleteQuest, packet.EncodeQuestgiverCompleteQuest(npcGUID, questID))
}

// ChooseQuestReward sends CMSG_QUESTGIVER_CHOOSE_REWARD. reward may be 0 when
// the quest offers no choice.
//
// AzerothCore rejects this if the quest is not in the reward-offer state, so it
// must only be sent after SMSG_QUESTGIVER_OFFER_REWARD.
func (s *Session) ChooseQuestReward(npcGUID uint64, questID uint32, reward uint32) error {
	return s.Send(CmsgQuestgiverChooseReward, packet.EncodeQuestgiverChooseReward(npcGUID, questID, reward))
}

// QueryQuestgiverStatusMultiple sends CMSG_QUESTGIVER_STATUS_MULTIPLE_QUERY,
// asking for the availability icons of every nearby quest giver at once. It is
// how a bot discovers who has work without walking up to each npc; the answer
// arrives as SMSG_QUESTGIVER_STATUS_MULTIPLE.
func (s *Session) QueryQuestgiverStatusMultiple() error {
	return s.Send(CmsgQuestgiverStatusMultipleQuery, packet.EncodeQuestgiverStatusMultipleQuery())
}

// GossipHello sends CMSG_GOSSIP_HELLO to open an npc's gossip menu.
func (s *Session) GossipHello(npcGUID uint64) error {
	return s.Send(CmsgGossipHello, packet.EncodeGossipHello(npcGUID))
}

// SelectGossipOption sends CMSG_GOSSIP_SELECT_OPTION.
//
// code is only meaningful for coded options; for any other option it must be
// the empty string so the payload ends after gossipListID, exactly as the real
// client sends it.
func (s *Session) SelectGossipOption(npcGUID uint64, menuID, gossipListID uint32, code string) error {
	return s.Send(CmsgGossipSelectOption, packet.EncodeGossipSelectOption(npcGUID, menuID, gossipListID, code))
}

// QueryNpcText sends CMSG_NPC_TEXT_QUERY for the text of a gossip option that
// is too long to fit in the menu packet itself.
func (s *Session) QueryNpcText(textID uint32, npcGUID uint64) error {
	return s.Send(CmsgNpcTextQuery, packet.EncodeNpcTextQuery(textID, npcGUID))
}
