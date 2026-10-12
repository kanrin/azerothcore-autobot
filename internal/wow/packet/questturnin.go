package packet

// The turn-in half of the quest conversation: the two dialogs a questgiver answers
// CMSG_QUESTGIVER_COMPLETE_QUEST with, and the reward confirmation.
//
//	SMSG_QUESTGIVER_REQUEST_ITEMS  GossipDef.cpp:752-800  PlayerMenu::SendQuestGiverRequestItems
//	SMSG_QUESTGIVER_OFFER_REWARD   GossipDef.cpp:647-700  PlayerMenu::SendQuestGiverOfferReward
//	SMSG_QUESTGIVER_QUEST_COMPLETE QuestPackets.cpp:27-37 QuestGiverQuestComplete::Write
//
// Which of the first two arrives is the answer to "is it done yet", and it is carried
// by the packet *type*, not by a field: SendQuestGiverRequestItems forwards to
// SendQuestGiverOfferReward when there is nothing left to hand over
// (GossipDef.cpp:771-775), so a bot reads "offer reward" as ready and "request items"
// as not yet.
//
// As with the accept side, the parsers stop after the prefix they need. The reward
// block that follows an offer is read only up to the choice items, because choosing one
// needs exactly that and nothing below it.

// QuestRequestItems is the prefix of SMSG_QUESTGIVER_REQUEST_ITEMS (0x18B): the dialog
// that lists what is still missing.
type QuestRequestItems struct {
	GiverGUID uint64
	QuestID   uint32
	Title     string

	// RequestText is the quest's CompletionText: the sentence a client shows above the
	// list of missing items, and the most useful thing to log.
	RequestText string
}

// QuestRewardItem is one line of a reward block: an item, how many, and the icon the
// client draws.
type QuestRewardItem struct {
	ItemID    uint32
	Count     uint32
	DisplayID uint32
}

// QuestOfferReward is the prefix of SMSG_QUESTGIVER_OFFER_REWARD (0x18D): the dialog
// whose "Complete quest" button sends CMSG_QUESTGIVER_CHOOSE_REWARD.
type QuestOfferReward struct {
	GiverGUID uint64
	QuestID   uint32
	Title     string

	// RewardText is the quest's OfferRewardText, shown above the rewards.
	RewardText string

	// AutoFinish is the `enableNext` byte: the client may skip the "continue" step.
	AutoFinish bool

	Flags            uint32
	SuggestedPlayers uint32

	// Choices is the list the player may pick one from. The index in this slice is
	// what CMSG_QUESTGIVER_CHOOSE_REWARD takes, which is why it is kept in order.
	Choices []QuestRewardItem
}

// QuestReward is SMSG_QUESTGIVER_QUEST_COMPLETE (0x191): what the reward turned out to
// be. AC QuestPackets.cpp:27-37 writes six uint32s in this order.
type QuestReward struct {
	QuestID      uint32
	Experience   uint32
	Money        uint32
	Honor        uint32
	TalentPoints uint32
	ArenaPoints  uint32
}

// ParseQuestGiverRequestItems decodes the prefix of SMSG_QUESTGIVER_REQUEST_ITEMS.
//
// AC GossipDef.cpp:791-796: guid, quest id, title, request text, then an unknown
// uint32. The rest of the packet describes the missing items, which nothing needs: the
// caller only has to know the server said "not yet" and why.
func ParseQuestGiverRequestItems(data []byte) (QuestRequestItems, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_REQUEST_ITEMS"}

	items := QuestRequestItems{
		GiverGUID:   q.guid(),
		QuestID:     q.u32(),
		Title:       q.cstr(),
		RequestText: q.cstr(),
	}
	return items, q.done()
}

// ParseQuestGiverOfferReward decodes the prefix of SMSG_QUESTGIVER_OFFER_REWARD.
//
// AC GossipDef.cpp:659-692, in order: guid, quest id, title, reward text, the
// auto-finish byte, flags, suggested players, then an emote count with that many
// { delay, emote } pairs, then the choice items as { item id, count, display id }. The
// fixed-item and money blocks that follow are not read: nothing in the flow needs them.
func ParseQuestGiverOfferReward(data []byte) (QuestOfferReward, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_OFFER_REWARD"}

	offer := QuestOfferReward{
		GiverGUID:        q.guid(),
		QuestID:          q.u32(),
		Title:            q.cstr(),
		RewardText:       q.cstr(),
		AutoFinish:       q.u8() != 0,
		Flags:            q.u32(),
		SuggestedPlayers: q.u32(),
	}

	emoteCount := q.u32()
	if q.err != nil {
		return offer, q.done()
	}
	for range emoteCount {
		q.u32() // delay
		q.u32() // emote
	}

	choiceCount := q.u32()
	if q.err != nil {
		return offer, q.done()
	}
	offer.Choices = make([]QuestRewardItem, 0, choiceCount)
	for range choiceCount {
		choice := QuestRewardItem{
			ItemID:    q.u32(),
			Count:     q.u32(),
			DisplayID: q.u32(),
		}
		if q.err != nil {
			return offer, q.done()
		}
		offer.Choices = append(offer.Choices, choice)
	}
	return offer, q.done()
}

// ParseQuestGiverQuestComplete decodes SMSG_QUESTGIVER_QUEST_COMPLETE (0x191).
//
// AC QuestPackets.cpp:27-37 writes quest id, experience, money, honor, talent points
// and arena points — all uint32 (QuestPackets.h:47-59).
func ParseQuestGiverQuestComplete(data []byte) (QuestReward, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_QUEST_COMPLETE"}

	reward := QuestReward{
		QuestID:      q.u32(),
		Experience:   q.u32(),
		Money:        q.u32(),
		Honor:        q.u32(),
		TalentPoints: q.u32(),
		ArenaPoints:  q.u32(),
	}
	return reward, q.done()
}
