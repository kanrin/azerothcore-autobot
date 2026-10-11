package state

import (
	"testing"

	"github.com/azerothcore/AzerothGhost/client"
)

// TestRead_Skills pins the PLAYER_SKILL_INFO_1_1 packing: three fields per
// skill, line|step, value|max, tempBonus|permBonus (Player.h:79-89).
func TestRead_Skills(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)
	// Slot 0: herbalism, 75 of 75, temporary -1 and permanent +2. The temporary
	// bonus is negative, which is why it has to travel through a variable: a
	// constant conversion of -1 to uint16 does not compile.
	var tempBonus int16 = -1
	putPair(self, playerSkillInfo11+0, 182, 0)
	putPair(self, playerSkillInfo11+1, 75, 75)
	putPair(self, playerSkillInfo11+2, uint16(tempBonus), 2)
	// Slot 1: unlearned, AzerothCore clears all three fields (Player.cpp:5539).
	// Slot 2: mining, 40 of 75, no bonus.
	putPair(self, playerSkillInfo11+2*3, 186, 1)
	putPair(self, playerSkillInfo11+2*3+1, 40, 75)

	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{selfGUID: self}}
	got := Read(src)

	if len(got.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2 (the cleared slot must be skipped): %+v", len(got.Skills), got.Skills)
	}
	herb := got.Skills[0]
	if herb.Line != 182 || herb.Step != 0 || herb.Value != 75 || herb.Max != 75 {
		t.Errorf("herbalism = %+v, want line 182 step 0 value 75/75", herb)
	}
	if herb.TempBonus != -1 || herb.PermBonus != 2 || herb.Total() != 76 {
		t.Errorf("herbalism bonuses = %+v total %d, want -1/+2 total 76", herb, herb.Total())
	}
	mine := got.Skills[1]
	if mine.Line != 186 || mine.Step != 1 || mine.Value != 40 || mine.Max != 75 {
		t.Errorf("mining = %+v, want line 186 step 1 value 40/75", mine)
	}
	if s, ok := got.Skill(182); !ok || s.Line != 182 {
		t.Errorf("Skill(182) = %+v, %v; want the herbalism entry", s, ok)
	}
	if _, ok := got.Skill(333); ok {
		t.Error("Skill(333) found an entry the character does not have")
	}
}

// TestRead_UnknownItem covers the gap between "the slot has a GUID" and "the
// item object is in the cache": the slot must be reported, flagged unknown, and
// must not contribute to Count.
func TestRead_UnknownItem(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)
	putGUID(self, playerFieldPackSlot1+1*2, 7001)

	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{selfGUID: self}}
	got := Read(src)

	if len(got.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.Known || item.Entry != 0 || item.GUID != 7001 {
		t.Errorf("item = %+v, want known=false entry=0 guid=7001", item)
	}
	if got.Count(7001) != 0 {
		t.Errorf("Count(7001) = %d, want 0 for an unresolved item", got.Count(7001))
	}
	if unknown := got.UnknownEntries(); len(unknown) != 1 || unknown[0] != 7001 {
		t.Errorf("UnknownEntries = %v, want [7001]", unknown)
	}
	if free := got.FreeBagSlots(); free != backpackSlots-1 {
		t.Errorf("FreeBagSlots = %d, want %d: an unresolved item still occupies its slot", free, backpackSlots-1)
	}
}
