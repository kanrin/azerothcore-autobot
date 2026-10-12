package packet

// SMSG_GOSSIP_MESSAGE (0x17D): the packet a creature answers a dialog with, and the
// one that actually carries a quest giver's quest list in most cases.
//
// AC GossipDef.cpp:197-234 (PlayerMenu::SendGossipMenu), in order:
//
//	uint64  objectGUID
//	uint32  menuId            (new in 2.4.0)
//	uint32  titleTextId
//	uint32  gossip option count
//	  × { uint32 gossipListID, uint8 icon, uint8 isCoded, uint32 boxMoney,
//	      CString message, CString boxMessage }
//	uint32  quest menu count
//	  × { uint32 questID, uint32 questIcon, int32 questLevel, uint32 questFlags,
//	      uint8 repeatable, CString title }
//
// The quest section is the same shape as one entry of SMSG_QUESTGIVER_QUEST_LIST
// (GossipDef.cpp:361-373), which is not a coincidence: both are built from the same
// QuestMenuItem. That is why this parser reuses QuestListEntry, and why a bot can take
// the quests from either packet without caring which one the NPC chose — a real
// question, since HandleQuestgiverHelloOpcode ends in SendPreparedGossip
// (QuestHandler.cpp:107) and a live run showed a plain quest giver answering with the
// gossip menu rather than the list.
//
// The option's *type* (GOSSIP_OPTION_QUESTGIVER and friends, GossipDef.h:35-50) is
// deliberately absent from the wire: the client knows it from its own copy of the
// gossip data. A bot that needs it has to read gossip_menu_option from the world
// database, which is what this project would do if it ever had to click an option.

// GossipOption is one line of a gossip menu.
type GossipOption struct {
	// ListID is the id to send back in CMSG_GOSSIP_SELECT_OPTION.
	ListID uint32

	Icon  uint8
	Coded bool

	// BoxMoney is the copper the option costs to open, if it is a coded option.
	BoxMoney uint32

	Message    string
	BoxMessage string
}

// GossipMessage is SMSG_GOSSIP_MESSAGE (0x17D).
type GossipMessage struct {
	GiverGUID uint64

	// MenuID is echoed back in CMSG_GOSSIP_SELECT_OPTION: the server drops the
	// selection unless it matches the menu it last sent.
	MenuID uint32

	TextID uint32

	Options []GossipOption

	// Quests is the quest menu the NPC has for this player, with the same fields a
	// quest list entry carries.
	Quests []QuestListEntry
}

// ParseGossipMessage decodes SMSG_GOSSIP_MESSAGE (0x17D).
func ParseGossipMessage(data []byte) (GossipMessage, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_GOSSIP_MESSAGE"}

	message := GossipMessage{
		GiverGUID: q.guid(),
		MenuID:    q.u32(),
		TextID:    q.u32(),
	}

	optionCount := q.u32()
	if q.err != nil {
		return message, q.done()
	}
	message.Options = make([]GossipOption, 0, optionCount)
	for range optionCount {
		option := GossipOption{
			ListID:     q.u32(),
			Icon:       q.u8(),
			Coded:      q.u8() != 0,
			BoxMoney:   q.u32(),
			Message:    q.cstr(),
			BoxMessage: q.cstr(),
		}
		if q.err != nil {
			return message, q.done()
		}
		message.Options = append(message.Options, option)
	}

	questCount := q.u32()
	if q.err != nil {
		return message, q.done()
	}
	message.Quests = make([]QuestListEntry, 0, questCount)
	for range questCount {
		entry := QuestListEntry{
			ID:         q.u32(),
			Icon:       q.u32(),
			Level:      q.i32(),
			Flags:      q.u32(),
			Repeatable: q.u8() != 0,
			Title:      q.cstr(),
		}
		if q.err != nil {
			return message, q.done()
		}
		message.Quests = append(message.Quests, entry)
	}
	return message, q.done()
}
