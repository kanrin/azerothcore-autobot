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

// QuestGiverQuestion is what opening a questgiver dialog yields: the quests the NPC
// has for this player, and which packet carried them.
type QuestGiverQuestion struct {
	// GiverGUID is the NPC that answered.
	GiverGUID uint64

	// Greeting is the NPC's line when there is one. For a gossip answer it is the
	// text id instead, which is why it is only ever logged.
	Greeting string

	// Quests is what the NPC offers or has in progress for this player.
	Quests []packet.QuestListEntry

	// AnsweredWith is the opcode that carried the list. It is recorded rather than
	// assumed because a live run showed a plain quest giver answering with the gossip
	// menu: the two paths are both real, and which one an NPC uses is server data.
	AnsweredWith uint16
}

// QuestGiverHello opens the quest dialog for one NPC and returns the quests it has.
//
// Three answers are possible, all of them in the AC source:
//
//	SMSG_QUESTGIVER_QUEST_LIST     the usual list (PlayerQuest.cpp:199)
//	SMSG_QUESTGIVER_QUEST_DETAILS  exactly one quest is relevant, so the server opens
//	                               that quest's dialog instead of a list (:148)
//	SMSG_GOSSIP_MESSAGE            the NPC has a gossip menu; HandleQuestgiverHelloOpcode
//	                               ends in SendPreparedGossip (QuestHandler.cpp:107) and
//	                               the menu's quest section carries the same entries
//
// The third is not a fallback: it is what the first live run of this feature met. All
// three carry the NPC's quest menu, so the caller gets the same thing either way.
func (s *Session) QuestGiverHello(ctx context.Context, npcGUID uint64, timeout time.Duration) (QuestGiverQuestion, error) {
	var out QuestGiverQuestion
	if npcGUID == 0 {
		return out, errors.New("wow: questgiver hello: guid is zero")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverHello, packet.EncodeQuestgiverHello(npcGUID),
		[]uint16{SmsgQuestgiverQuestList, SmsgGossipMessage, SmsgQuestgiverQuestDetails}, timeout)
	if err != nil {
		return out, fmt.Errorf("wow: questgiver hello: %w", err)
	}

	out.GiverGUID, out.AnsweredWith = npcGUID, opcode
	switch opcode {
	case SmsgQuestgiverQuestList:
		list, parseErr := packet.ParseQuestGiverQuestList(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Greeting, out.Quests = list.Greeting, list.Entries

	case SmsgGossipMessage:
		message, parseErr := packet.ParseGossipMessage(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Greeting = fmt.Sprintf("gossip text %d, %d options", message.TextID, len(message.Options))
		out.Quests = message.Quests

	case SmsgQuestgiverQuestDetails:
		details, parseErr := packet.ParseQuestGiverQuestDetails(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Greeting = details.Title
		out.Quests = []packet.QuestListEntry{{
			ID: details.QuestID, Title: details.Title,
		}}

	default:
		return out, fmt.Errorf("wow: questgiver hello: unexpected reply %s", OpcodeName(opcode))
	}
	return out, nil
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

// QuestTurnIn is what the server answers "here is my finished quest" with.
type QuestTurnIn struct {
	QuestID uint32
	Title   string

	// Ready is true when the server offered the reward, i.e. the quest can be handed
	// in. False means it answered with the requirements instead — the same dialog a
	// player sees when something is still missing.
	Ready bool

	// Text is the reward text when Ready and the completion text when not, which is
	// the most useful thing to put in a log line.
	Text string

	// Choices is how many reward options the offer carries (0 for most quests).
	Choices int

	// AnsweredWith is the opcode, recorded because which dialog arrives is the answer
	// to "is it done yet" and is worth seeing in the log.
	AnsweredWith uint16
}

// CompleteQuest tells a questgiver that a quest is finished and reports whether the
// server agreed.
//
// AC QuestHandler.cpp:478-521 — the NPC has to be involved in the quest (an ender, not
// just any questgiver) and in interaction range; the answer is
// SMSG_QUESTGIVER_OFFER_REWARD when it is ready (SendQuestGiverRequestItems forwards to
// it as soon as nothing is left to hand over, GossipDef.cpp:771-775) or
// SMSG_QUESTGIVER_REQUEST_ITEMS with the reason when it is not.
//
// A silent answer means the NPC was not an ender, was out of range, or the quest is not
// in the log: all three make the handler return without a packet (QuestHandler.cpp:488-502).
func (s *Session) CompleteQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (QuestTurnIn, error) {
	var out QuestTurnIn
	if npcGUID == 0 || questID == 0 {
		return out, errors.New("wow: complete quest: guid and quest id are required")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverCompleteQuest,
		packet.EncodeQuestgiverCompleteQuest(npcGUID, questID),
		[]uint16{SmsgQuestgiverOfferReward, SmsgQuestgiverRequestItems}, timeout)
	if err != nil {
		return out, fmt.Errorf("wow: complete quest %d: %w", questID, err)
	}

	out.QuestID, out.AnsweredWith = questID, opcode
	switch opcode {
	case SmsgQuestgiverOfferReward:
		offer, parseErr := packet.ParseQuestGiverOfferReward(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Ready, out.Title, out.Text, out.Choices = true, offer.Title, offer.RewardText, len(offer.Choices)

	case SmsgQuestgiverRequestItems:
		items, parseErr := packet.ParseQuestGiverRequestItems(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Title, out.Text = items.Title, items.RequestText

	default:
		// Do only returns a listed opcode, so this is unreachable; saying so beats
		// silently reporting a half-filled struct.
		return out, fmt.Errorf("wow: complete quest %d: unexpected reply %s", questID, OpcodeName(opcode))
	}
	return out, nil
}

// RequestReward asks for the reward dialog of a quest whose items are in hand.
//
// It is the missing half of the hand-in for a quest that requires items: sending
// CMSG_QUESTGIVER_COMPLETE_QUEST for such a quest answers with SMSG_QUESTGIVER_REQUEST_ITEMS
// even when the player has everything (QuestHandler.cpp:517-518 routes on
// `quest->GetReqItemsCount()`), and the client's "Complete Quest" button sends *this*
// packet, which answers with SMSG_QUESTGIVER_OFFER_REWARD (QuestHandler.cpp:355-379).
//
// The handler returns silently when the quest is not complete, so a caller should only
// ask after the quest log says the objectives are done; the timeout then means "the
// server did not consider it finished".
func (s *Session) RequestReward(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (QuestTurnIn, error) {
	var out QuestTurnIn
	if npcGUID == 0 || questID == 0 {
		return out, errors.New("wow: request reward: guid and quest id are required")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverRequestReward,
		packet.EncodeQuestgiverRequestReward(npcGUID, questID),
		[]uint16{SmsgQuestgiverOfferReward, SmsgQuestgiverRequestItems}, timeout)
	if err != nil {
		return out, fmt.Errorf("wow: request reward for quest %d: %w", questID, err)
	}

	out.QuestID, out.AnsweredWith = questID, opcode
	switch opcode {
	case SmsgQuestgiverOfferReward:
		offer, parseErr := packet.ParseQuestGiverOfferReward(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Ready, out.Title, out.Text, out.Choices = true, offer.Title, offer.RewardText, len(offer.Choices)

	case SmsgQuestgiverRequestItems:
		items, parseErr := packet.ParseQuestGiverRequestItems(data)
		if parseErr != nil {
			return out, parseErr
		}
		out.Title, out.Text = items.Title, items.RequestText

	default:
		return out, fmt.Errorf("wow: request reward for quest %d: unexpected reply %s",
			questID, OpcodeName(opcode))
	}
	return out, nil
}

// ChooseReward takes one of the offered rewards.
//
// Two confirmations are required, and both are real signals: SMSG_QUESTGIVER_QUEST_COMPLETE
// carries what was granted (QuestPackets.cpp:27-37), and the quest leaving the quest log
// is the state change a client shows. Waiting for the log too means a reward that the
// server silently refused is not reported as taken.
//
// reward is the index into the offer's choice list (0 when there is no choice), and
// AzerothCore rejects anything from QUEST_REWARD_CHOICES_COUNT (6) up
// (QuestHandler.cpp:255-263).
func (s *Session) ChooseReward(ctx context.Context, npcGUID uint64, questID, reward uint32, timeout time.Duration) (packet.QuestReward, error) {
	var out packet.QuestReward
	if npcGUID == 0 || questID == 0 {
		return out, errors.New("wow: choose reward: guid and quest id are required")
	}
	if timeout <= 0 {
		timeout = DefaultQuestTimeout
	}

	opcode, data, err := s.Do(ctx, CmsgQuestgiverChooseReward,
		packet.EncodeQuestgiverChooseReward(npcGUID, questID, reward),
		[]uint16{SmsgQuestgiverQuestComplete}, timeout)
	if err != nil {
		return out, fmt.Errorf("wow: choose reward for quest %d: %w", questID, err)
	}
	if opcode != SmsgQuestgiverQuestComplete {
		return out, fmt.Errorf("wow: choose reward for quest %d: unexpected reply %s",
			questID, OpcodeName(opcode))
	}

	if out, err = packet.ParseQuestGiverQuestComplete(data); err != nil {
		return out, err
	}
	if out.QuestID != questID {
		return out, fmt.Errorf("wow: choose reward: the confirmation is for quest %d, asked about %d",
			out.QuestID, questID)
	}

	if err := s.WaitFor(ctx, timeout, fmt.Sprintf("quest %d to leave the quest log", questID),
		func() bool { return !state.Read(s).HasQuest(questID) }); err != nil {
		return out, fmt.Errorf("wow: choose reward for quest %d: %w", questID, err)
	}
	return out, nil
}

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
