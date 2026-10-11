package wow

import (
	"azerothcore-autobot/internal/wow/packet"
)

// Send-only wrappers for the quest and gossip packets this project implements
// itself.
//
// Each wrapper is deliberately dumb: it encodes and hands the bytes to Session,
// which serialises the write and refuses to send on a closed session. The
// deliberate omission of a reply wait is what distinguishes these from the
// query/handshake calls in the quest feature, which use Session.Do so the
// listener is armed before the packet leaves.
//
// Every payload is produced by internal/wow/packet, which carries the golden
// byte tests transcribed from AzerothCore. Do not hand-roll bytes here.

// QueryQuestDefinition sends CMSG_QUEST_QUERY for the static definition of a
// quest (level, objectives, rewards) — the data the bot needs before it can
// decide whether a quest is worth taking.
func (s *Session) QueryQuestDefinition(questID uint32) error {
	return s.Send(CmsgQuestQuery, packet.EncodeQuestQuery(questID))
}

// GreetQuestgiver sends CMSG_QUESTGIVER_HELLO to open the quest dialogue with a
// creature. The reply is either SMSG_QUESTGIVER_QUEST_LIST (the giver has
// quests) or SMSG_QUESTGIVER_QUEST_DETAILS when there is exactly one.
func (s *Session) GreetQuestgiver(npcGUID uint64) error {
	return s.Send(CmsgQuestgiverHello, packet.EncodeQuestgiverHello(npcGUID))
}

// QueryQuestgiverQuest sends CMSG_QUESTGIVER_QUERY_QUEST to fetch the details or
// the completion requirements of one quest from a creature.
func (s *Session) QueryQuestgiverQuest(npcGUID uint64, questID uint32) error {
	return s.Send(CmsgQuestgiverQueryQuest, packet.EncodeQuestgiverQueryQuest(npcGUID, questID, 0))
}

// AcceptQuest sends CMSG_QUESTGIVER_ACCEPT_QUEST.
func (s *Session) AcceptQuest(npcGUID uint64, questID uint32) error {
	return s.Send(CmsgQuestgiverAcceptQuest, packet.EncodeQuestgiverAcceptQuest(npcGUID, questID))
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
