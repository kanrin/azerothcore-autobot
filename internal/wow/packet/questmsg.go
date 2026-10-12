package packet

import "fmt"

// Quest server-packet parsers: the SMSG half of the quest conversation.
//
// Every layout is transcribed from the AzerothCore code that *writes* the packet,
// because that is what fixes the byte order:
//
//	SMSG_QUESTGIVER_QUEST_LIST     GossipDef.cpp:315-375  PlayerMenu::SendQuestGiverQuestList
//	SMSG_QUESTGIVER_QUEST_DETAILS  GossipDef.cpp:404-500  PlayerMenu::SendQuestGiverQuestDetails
//	SMSG_QUESTGIVER_QUEST_INVALID  QuestPackets.cpp:54-59 QuestGiverQuestInvalid::Write
//	SMSG_QUESTUPDATE_ADD_KILL      QuestPackets.cpp:78-88 QuestUpdateAddKill::Write
//	SMSG_QUESTUPDATE_COMPLETE/FAILED/FAILEDTIMER  QuestPackets.cpp:20-52
//
// The parsers read the prefix they need and ignore the tail where the tail is a
// reward block nothing consumes yet. That is safe by construction — a reader that
// stops early cannot misread a later field — and it is written down here so the
// next field added comes with its own AC line reference.
//
// What these packets do *not* carry is the quest log itself: the log lives in the
// player's update fields (PLAYER_QUEST_LOG_1_1 + slot*5, UpdateFields.h:186),
// which is why internal/wow/state reads it from the object cache instead.

// QuestListEntry is one line of the questgiver's list: the "!" and "?" entries a
// client shows in the quest dialog.
type QuestListEntry struct {
	ID   uint32
	Icon uint32

	// Level is the quest's level, signed on the wire (a quest can be level -1).
	Level int32

	Flags uint32

	// Repeatable drives the blue question mark of a repeatable quest.
	Repeatable bool

	Title string
}

// QuestList is SMSG_QUESTGIVER_QUEST_LIST (0x185).
type QuestList struct {
	GiverGUID uint64

	// Greeting is the NPC's line, from quest_greeting when the creature has one
	// and the generic title otherwise.
	Greeting string

	EmoteDelay uint32
	EmoteType  uint32

	Entries []QuestListEntry
}

// QuestDetails is the prefix of SMSG_QUESTGIVER_QUEST_DETAILS (0x188) that a
// client needs to show the "Accept / Decline" dialog.
type QuestDetails struct {
	GiverGUID uint64

	// DividerGUID is the player being helped when the quest was shared; zero
	// otherwise. It is the field the client echoes back in the accept packet.
	DividerGUID uint64

	QuestID uint32

	Title      string
	Details    string
	Objectives string

	// AutoAccept is `activateAccept`: the dialog opens with the accept button
	// already active, which the server sets for quests with no requirements.
	AutoAccept bool

	Flags            uint32
	SuggestedPlayers uint32
}

// QuestInvalid is SMSG_QUESTGIVER_QUEST_INVALID (0x18F): the reason a quest
// interaction was refused. The payload is the reason alone, with no quest id —
// which is why the caller has to remember which quest it asked about.
type QuestInvalid struct {
	Reason uint32
}

// QuestAddKill is SMSG_QUESTUPDATE_ADD_KILL (0x199): one kill counted toward a
// quest objective.
type QuestAddKill struct {
	QuestID       uint32
	CreatureEntry uint32
	Current       uint32
	Required      uint32
	ObjectiveGUID uint64
}

// QuestUpdate is the payload of the three notification-only quest updates:
// SMSG_QUESTUPDATE_COMPLETE (0x198), _FAILED (0x196) and _FAILEDTIMER (0x197).
// All three carry nothing but the quest id.
type QuestUpdate struct {
	QuestID uint32
}

// questReader reads a run of fields and checks the error once at the end.
//
// The point is readability of the *order*: the field order is the thing that has
// to match AzerothCore, and interleaving an error check after every field buries
// it. Every accessor is a no-op once the first read failed, so the worst case is a
// zero value the caller never sees, because it gets the error instead.
type questReader struct {
	r    *Reader
	what string
	err  error
}

func (q *questReader) u8() uint8 {
	if q.err != nil {
		return 0
	}
	v, err := q.r.Uint8()
	if err != nil {
		q.err = err
	}
	return v
}

func (q *questReader) u32() uint32 {
	if q.err != nil {
		return 0
	}
	v, err := q.r.Uint32()
	if err != nil {
		q.err = err
	}
	return v
}

func (q *questReader) i32() int32 { return int32(q.u32()) }

func (q *questReader) guid() uint64 {
	if q.err != nil {
		return 0
	}
	v, err := q.r.RawGUID()
	if err != nil {
		q.err = err
	}
	return v
}

func (q *questReader) cstr() string {
	if q.err != nil {
		return ""
	}
	v, err := q.r.CString()
	if err != nil {
		q.err = err
	}
	return v
}

// done wraps the first error with the packet's name.
func (q *questReader) done() error {
	if q.err == nil {
		return nil
	}
	return fmt.Errorf("packet: %s: %w", q.what, q.err)
}

// ParseQuestGiverQuestList decodes SMSG_QUESTGIVER_QUEST_LIST (0x185).
//
// AC GossipDef.cpp:317-373, in order: guid, greeting (C string), emote delay
// (uint32), emote type (uint32), then a uint8 count followed by that many
// { quest id, icon, level (int32), flags, repeatable (uint8), title (C string) }.
func ParseQuestGiverQuestList(data []byte) (QuestList, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_QUEST_LIST"}

	list := QuestList{
		GiverGUID:  q.guid(),
		Greeting:   q.cstr(),
		EmoteDelay: q.u32(),
		EmoteType:  q.u32(),
	}
	count := q.u8()
	if q.err != nil {
		return list, q.done()
	}

	list.Entries = make([]QuestListEntry, 0, count)
	for range count {
		entry := QuestListEntry{
			ID:         q.u32(),
			Icon:       q.u32(),
			Level:      q.i32(),
			Flags:      q.u32(),
			Repeatable: q.u8() != 0,
			Title:      q.cstr(),
		}
		if q.err != nil {
			return list, q.done()
		}
		list.Entries = append(list.Entries, entry)
	}
	return list, q.done()
}

// ParseQuestGiverQuestDetails decodes the prefix of SMSG_QUESTGIVER_QUEST_DETAILS
// (0x188) that identifies the quest and fills the dialog.
//
// AC GossipDef.cpp:405-413, in order: npc guid, the divider guid (the player a
// shared quest came from, zero otherwise), quest id, title, details, objectives
// and the `activateAccept` byte. The reward block that follows (choice items,
// items, money, xp, spells, reputations, emotes — GossipDef.cpp:414-500) is not
// read: nothing needs it to accept a quest.
func ParseQuestGiverQuestDetails(data []byte) (QuestDetails, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_QUEST_DETAILS"}

	details := QuestDetails{
		GiverGUID:        q.guid(),
		DividerGUID:      q.guid(),
		QuestID:          q.u32(),
		Title:            q.cstr(),
		Details:          q.cstr(),
		Objectives:       q.cstr(),
		AutoAccept:       q.u8() != 0,
		Flags:            q.u32(),
		SuggestedPlayers: q.u32(),
	}
	return details, q.done()
}

// ParseQuestGiverQuestInvalid decodes SMSG_QUESTGIVER_QUEST_INVALID (0x18F).
//
// AC QuestPackets.cpp:54-59 writes the failure reason and nothing else.
func ParseQuestGiverQuestInvalid(data []byte) (QuestInvalid, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTGIVER_QUEST_INVALID"}
	invalid := QuestInvalid{Reason: q.u32()}
	return invalid, q.done()
}

// ParseQuestUpdateAddKill decodes SMSG_QUESTUPDATE_ADD_KILL (0x199).
//
// AC QuestPackets.cpp:78-88: quest id, creature entry, current count, required
// count, then the objective's guid as a raw uint64.
func ParseQuestUpdateAddKill(data []byte) (QuestAddKill, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTUPDATE_ADD_KILL"}
	kill := QuestAddKill{
		QuestID:       q.u32(),
		CreatureEntry: q.u32(),
		Current:       q.u32(),
		Required:      q.u32(),
		ObjectiveGUID: q.guid(),
	}
	return kill, q.done()
}

// ParseQuestUpdate decodes the quest-id-only updates: SMSG_QUESTUPDATE_COMPLETE
// (0x198), _FAILED (0x196) and _FAILEDTIMER (0x197).
//
// AC QuestPackets.cpp:20-52 — all three write a single uint32.
func ParseQuestUpdate(data []byte) (QuestUpdate, error) {
	q := &questReader{r: NewReader(data), what: "SMSG_QUESTUPDATE_*"}
	update := QuestUpdate{QuestID: q.u32()}
	return update, q.done()
}
