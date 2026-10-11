package state

import (
	"testing"

	"github.com/azerothcore/AzerothGhost/client"
)

// TestRead_EquipmentAndBank pins the INV_SLOT_HEAD equipment mapping and the two
// bank arrays, which later features (repair, mail, auction) walk.
func TestRead_EquipmentAndBank(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)
	putGUID(self, playerFieldInvSlotHead+0*2, 8001)  // main hand
	putGUID(self, playerFieldBankSlot1+3*2, 8002)    // bank slot 3
	putGUID(self, playerFieldBankBagSlot1+0*2, 8003) // bank bag 0

	bankBag := newObj(8003, 4496, client.ObjectTypeContainer)
	bankBag.Values[containerFieldNumSlots] = 2
	putGUID(bankBag, containerFieldSlot1+0*2, 8004)

	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{
		selfGUID: self,
		8001:     newObj(8001, 25, client.ObjectTypeItem),
		8002:     newObj(8002, 2589, client.ObjectTypeItem),
		8003:     bankBag,
		8004:     newObj(8004, 2592, client.ObjectTypeItem),
	}}
	got := Read(src)

	if len(got.Items) != 4 {
		t.Fatalf("len(Items) = %d, want 4: %+v", len(got.Items), got.Items)
	}
	want := []SlotRef{
		{Container: ContainerEquipped, Slot: 0},
		{Container: ContainerBank, Slot: 3},
		{Container: ContainerBankBag, Bag: 0},
		{Container: ContainerBankBag, Bag: 0, Slot: 0},
	}
	for i, ref := range want {
		if got.Items[i].Slot != ref {
			t.Errorf("item %d slot = %v, want %v", i, got.Items[i].Slot, ref)
		}
	}
	// Bank slots are not part of "can I keep looting": only the backpack and the
	// equipped bags count.
	if len(got.Containers) != 1 || got.Containers[0].Slot.Container != ContainerBackpack {
		t.Errorf("Containers = %+v, want only the empty backpack", got.Containers)
	}
	if free := got.FreeBagSlots(); free != backpackSlots {
		t.Errorf("FreeBagSlots = %d, want %d (bank excluded)", free, backpackSlots)
	}
}

// TestCount_StacksAndSingleItems checks the stack-count-zero convention and that
// identical entries in different containers are summed.
func TestCount_StacksAndSingleItems(t *testing.T) {
	snap := Snapshot{Items: []Item{
		{Slot: SlotRef{Container: ContainerBackpack, Slot: 0}, Entry: 2447, Count: 20, Known: true},
		{Slot: SlotRef{Container: ContainerBackpack, Slot: 1}, Entry: 2447, Count: 0, Known: true},
		{Slot: SlotRef{Container: ContainerBank, Slot: 0}, Entry: 2447, Count: 5, Known: true},
		{Slot: SlotRef{Container: ContainerBackpack, Slot: 2}, Entry: 118, Count: 3},
	}}
	if got := snap.Count(2447); got != 26 {
		t.Errorf("Count(2447) = %d, want 26 (a single item counts as one)", got)
	}
	if got := snap.Count(0); got != 0 {
		t.Errorf("Count(0) = %d, want 0", got)
	}
	if got := snap.Count(118); got != 0 {
		t.Errorf("Count(118) = %d, want 0 for an unresolved item", got)
	}
}

// TestSnapshot_String keeps the log line parseable for the run summary.
func TestSnapshot_String(t *testing.T) {
	snap := Snapshot{
		Money:      42,
		Items:      []Item{{Slot: SlotRef{Container: ContainerBackpack, Slot: 0}, Entry: 1, Known: true}},
		Skills:     []Skill{{Line: 182, Value: 75}},
		Containers: []ContainerInfo{{Slot: SlotRef{Container: ContainerBackpack}, Capacity: 16, Used: 1}},
	}
	const want = "money=42 items=1 free_bag_slots=15 skills=182:75"
	if got := snap.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := (Snapshot{}).String(); got != "money=0 items=0 free_bag_slots=0 skills=none" {
		t.Errorf("String() of an empty snapshot = %q", got)
	}
}
