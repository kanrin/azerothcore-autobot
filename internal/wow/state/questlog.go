package state

import "github.com/azerothcore/AzerothGhost/client"

// The quest log, read from the player's own update fields.
//
// There is no "here is your quest log" packet in 3.3.5a: the log *is* an array of
// update fields in the player object, which is why accepting a quest is confirmed
// by this cache changing rather than by a reply packet — the server answers
// CMSG_QUESTGIVER_ACCEPT_QUEST with SMSG_GOSSIP_COMPLETE in both the accepted and
// the refused case (QuestHandler.cpp:113-197), and the only difference is whether
// a quest slot filled in.
//
// Layout, all from AzerothCore:
//
//	UpdateFields.h:186   PLAYER_QUEST_LOG_1_1 = UNIT_END + 0x000A, and :285
//	                     PLAYER_QUEST_LOG_25_5 = UNIT_END + 0x0086 → 25 slots.
//	Player.h:615         MAX_QUEST_OFFSET = 5 fields per slot.
//	Player.h:609-612     QUEST_ID_OFFSET 0, QUEST_STATE_OFFSET 1,
//	                     QUEST_COUNTS_OFFSET 2, QUEST_TIME_OFFSET 4. Offsets 2 and
//	                     3 are one uint64: four uint16 counters
//	                     (Player.h:1512 shifts by counter*16).
//	Player.h:619-621     QUEST_STATE_NONE / COMPLETE / FAIL. SetQuestSlotState is
//	                     SetFlag (Player.h:1529), so it is a mask, not an enum.
const (
	questLogHead   = unitEnd + 0x000A // UpdateFields.h:186
	questLogStride = 5                // Player.h:615
	questLogSlots  = 25               // UpdateFields.h:186-285

	questLogIDOffset     = 0 // Player.h:609
	questLogStateOffset  = 1 // Player.h:610
	questLogCountsOffset = 2 // Player.h:611
	questLogTimeOffset   = 4 // Player.h:612
)

// Quest log state flags (Player.h:619-621).
const (
	QuestStateNone     uint32 = 0x0000
	QuestStateComplete uint32 = 0x0001
	QuestStateFail     uint32 = 0x0002
)

// QuestCounters is how many objective counters one quest slot carries
// (Player.h:1512 reads counter 0..3 out of the 64-bit counts field).
const QuestCounters = 4

// QuestSlot is one entry of the player's quest log.
type QuestSlot struct {
	// Index is the log position, 0-based (the server's slot 0 is
	// PLAYER_QUEST_LOG_1_1).
	Index uint8

	QuestID uint32

	// State is the raw flag field: see QuestStateNone/Complete/Fail.
	State uint32

	// Counters is the per-objective progress. Unused entries stay zero; which
	// counter belongs to which objective comes from the quest's objectives, which
	// is server-side data (quest_template).
	Counters [QuestCounters]uint16

	// Time is the accept timestamp, used by the server for timed quests.
	Time uint32
}

// IsComplete reports whether the server marked every objective done — the state
// that shows a yellow "?" over the questgiver.
func (q QuestSlot) IsComplete() bool { return q.State&QuestStateComplete != 0 }

// Counter returns one objective's progress, or 0 when the index is out of range.
//
// The value is a plain count — "3 of the 8 wolves so far" — not a count/total
// pair: Player.h:1522-1527 writes a single uint16 into one of four 16-bit lanes,
// and the total comes from the quest template (RequiredNpcOrGoCount /
// RequiredItemCount in acore_world.quest_template), not from the client. Reading a
// lane as a packed value is the kind of mistake that yields a plausible wrong
// number, so it is spelled out here.
func (q QuestSlot) Counter(i int) uint16 {
	if i < 0 || i >= QuestCounters {
		return 0
	}
	return q.Counters[i]
}

// readQuestLog collects the occupied quest slots.
//
// Empty slots are skipped rather than returned as "quest 0": a caller asking "am I
// on this quest" must not have to know that an unused slot reads as zero.
func readQuestLog(self *client.WorldObject) []QuestSlot {
	if self == nil {
		return nil
	}

	slots := make([]QuestSlot, 0, questLogSlots)
	for i := range questLogSlots {
		base := uint16(questLogHead + i*questLogStride)

		questID := self.Value(base + questLogIDOffset)
		if questID == 0 {
			continue
		}

		slot := QuestSlot{
			Index:   uint8(i),
			QuestID: questID,
			State:   self.Value(base + questLogStateOffset),
			Time:    self.Value(base + questLogTimeOffset),
		}
		// The two counter fields are one uint64, low half first.
		counts := uint64(self.Value(base+questLogCountsOffset)) |
			uint64(self.Value(base+questLogCountsOffset+1))<<32
		for c := range QuestCounters {
			slot.Counters[c] = uint16(counts >> (c * 16))
		}

		slots = append(slots, slot)
	}
	return slots
}

// Quest returns the log entry for one quest.
func (snap Snapshot) Quest(questID uint32) (QuestSlot, bool) {
	for _, slot := range snap.Quests {
		if slot.QuestID == questID {
			return slot, true
		}
	}
	return QuestSlot{}, false
}

// HasQuest reports whether the quest is in the log, which is the client-side form
// of "the bot is on this quest".
func (snap Snapshot) HasQuest(questID uint32) bool {
	_, ok := snap.Quest(questID)
	return ok
}

// QuestIDs returns the quest ids in the log, in slot order. It is what a planner
// compares against the quests an NPC offers.
func (snap Snapshot) QuestIDs() []uint32 {
	ids := make([]uint32, 0, len(snap.Quests))
	for _, slot := range snap.Quests {
		ids = append(ids, slot.QuestID)
	}
	return ids
}
