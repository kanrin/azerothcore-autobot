package state

import (
	"testing"

	"github.com/azerothcore/AzerothGhost/client"
)

// putQuest writes one quest log slot the way the server does: id at offset 0,
// state at 1, the four counters packed two per field at 2 and 3, time at 4
// (Player.h:609-612, 1512).
func putQuest(o *client.WorldObject, slot int, questID, state uint32, counters [QuestCounters]uint16, at uint32) {
	base := uint16(questLogHead + slot*questLogStride)
	o.Values[base+questLogIDOffset] = questID
	o.Values[base+questLogStateOffset] = state
	putPair(o, base+questLogCountsOffset, counters[0], counters[1])
	putPair(o, base+questLogCountsOffset+1, counters[2], counters[3])
	o.Values[base+questLogTimeOffset] = at
}

// TestRead_QuestLog pins the layout: five fields per slot, the counters packed
// four to a uint64, and empty slots skipped.
func TestRead_QuestLog(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)

	// Slot 0: a kill quest in progress, 4 of 10 killed, second objective unused.
	putQuest(self, 0, 18, QuestStateNone, [QuestCounters]uint16{0x000A, 0x0004}, 0)
	// Slot 3: every objective done ("yellow question mark").
	putQuest(self, 3, 783, QuestStateComplete, [QuestCounters]uint16{}, 1700000000)
	// Slot 24 is the last one the layout has; anything past it must not be read.
	putQuest(self, 24, 5261, QuestStateFail, [QuestCounters]uint16{1, 2, 3, 4}, 7)

	// A field one slot past the end, to prove the reader stops at 25.
	self.Values[uint16(questLogHead+questLogSlots*questLogStride+questLogIDOffset)] = 99999

	snap := Read(fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{selfGUID: self}})

	if len(snap.Quests) != 3 {
		t.Fatalf("quest log has %d entries, want 3: %+v", len(snap.Quests), snap.Quests)
	}
	if snap.Quests[0].Index != 0 || snap.Quests[1].Index != 3 || snap.Quests[2].Index != 24 {
		t.Errorf("indices = %d/%d/%d, want 0/3/24",
			snap.Quests[0].Index, snap.Quests[1].Index, snap.Quests[2].Index)
	}

	kill := snap.Quests[0]
	if kill.QuestID != 18 {
		t.Errorf("slot 0 quest = %d, want 18", kill.QuestID)
	}
	if kill.Counters[0] != 0x000A || kill.Counters[1] != 0x0004 {
		t.Errorf("counters = %v, want [10 4 0 0]: one count per objective lane", kill.Counters)
	}
	if kill.Counter(0) != 0x000A || kill.Counter(1) != 0x0004 {
		t.Errorf("Counter(0/1) = %d/%d, want 10/4", kill.Counter(0), kill.Counter(1))
	}
	if kill.IsComplete() {
		t.Error("slot 0 is marked complete, want incomplete")
	}

	if !snap.Quests[1].IsComplete() {
		t.Error("slot 3 has state COMPLETE and must report IsComplete")
	}
	if snap.Quests[1].Time != 1700000000 {
		t.Errorf("slot 3 time = %d, want 1700000000", snap.Quests[1].Time)
	}

	last := snap.Quests[2]
	if last.Counters != [QuestCounters]uint16{1, 2, 3, 4} {
		t.Errorf("slot 24 counters = %v, want [1 2 3 4]", last.Counters)
	}
	if last.State&QuestStateFail == 0 {
		t.Errorf("slot 24 state = %#x, want the FAIL flag set", last.State)
	}
}

// TestSnapshot_QuestLookups covers the helpers a planner uses to answer "am I on
// this quest" and "which quests am I on".
func TestSnapshot_QuestLookups(t *testing.T) {
	snap := Snapshot{Quests: []QuestSlot{
		{Index: 0, QuestID: 783},
		{Index: 1, QuestID: 18, State: QuestStateComplete},
	}}

	if !snap.HasQuest(783) {
		t.Error("HasQuest(783) = false, want true")
	}
	if snap.HasQuest(999) {
		t.Error("HasQuest(999) = true, want false")
	}
	slot, ok := snap.Quest(18)
	if !ok || !slot.IsComplete() {
		t.Errorf("Quest(18) = %+v/%v, want the completed slot", slot, ok)
	}
	if ids := snap.QuestIDs(); len(ids) != 2 || ids[0] != 783 || ids[1] != 18 {
		t.Errorf("QuestIDs = %v, want [783 18]", ids)
	}

	// An index outside the four counters reads as zero rather than panicking: the
	// callers are planners that may look at one objective too many.
	if got := slot.Counter(4); got != 0 {
		t.Errorf("Counter(4) = %d, want 0", got)
	}
	if got := slot.Counter(-1); got != 0 {
		t.Errorf("Counter(-1) = %d, want 0", got)
	}
}

// TestReadQuestLog_NoObject keeps the reader safe before login and right after a
// teleport cleared the cache.
func TestReadQuestLog_NoObject(t *testing.T) {
	if got := readQuestLog(nil); got != nil {
		t.Errorf("readQuestLog(nil) = %v, want nil", got)
	}
	if got := Read(nil); got.Quests != nil {
		t.Errorf("Read(nil).Quests = %v, want nil", got.Quests)
	}
}
