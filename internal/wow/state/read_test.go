package state

import (
	"testing"

	"github.com/azerothcore/AzerothGhost/client"
)

// fakeSource is a Source over a map, so the tests exercise the real field
// offsets without a server.
type fakeSource struct {
	self uint64
	objs map[uint64]*client.WorldObject
}

func (f fakeSource) CharGUID() uint64 { return f.self }

func (f fakeSource) Object(guid uint64) *client.WorldObject { return f.objs[guid] }

// newObj builds a tracked object the way AzerothGhost's cache does.
func newObj(guid uint64, entry uint32, typeID uint8) *client.WorldObject {
	return &client.WorldObject{
		GUID:   guid,
		Entry:  entry,
		TypeID: typeID,
		Values: map[uint16]uint32{},
	}
}

// putGUID writes a two-field ObjectGuid, mirroring WorldObject.GUIDField.
func putGUID(o *client.WorldObject, field uint16, guid uint64) {
	o.Values[field] = uint32(guid & 0xFFFFFFFF)
	o.Values[field+1] = uint32(guid >> 32)
}

// putPair writes a packed two-short field, the TOW_SHORT layout AzerothCore
// uses for skills and item enchantments.
func putPair(o *client.WorldObject, field uint16, lo, hi uint16) {
	o.Values[field] = uint32(lo) | uint32(hi)<<16
}

const selfGUID = 1000

// TestRead_NoSelfObject covers the state before login and right after a
// cross-map teleport, where the cache holds no self object: the snapshot must be
// zero, and callers must read that as "unknown".
func TestRead_NoSelfObject(t *testing.T) {
	if got := Read(nil); got.Money != 0 || len(got.Items) != 0 || len(got.Skills) != 0 {
		t.Fatalf("Read(nil) = %+v, want zero snapshot", got)
	}
	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{}}
	if got := Read(src); got.Money != 0 || len(got.Items) != 0 {
		t.Fatalf("Read(empty cache) = %+v, want zero snapshot", got)
	}
}

// TestRead_MoneyAndBackpack pins PLAYER_FIELD_COINAGE and the backpack slot
// mapping: slot 0 is the first GUID pair of PLAYER_FIELD_PACK_SLOT_1.
func TestRead_MoneyAndBackpack(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)
	self.Values[playerFieldCoinage] = 1234
	putGUID(self, playerFieldPackSlot1+0*2, 5001)
	putGUID(self, playerFieldPackSlot1+5*2, 5002)

	potion := newObj(5001, 118, client.ObjectTypeItem)
	potion.Values[itemFieldStackCount] = 5
	herb := newObj(5002, 2447, client.ObjectTypeItem)
	herb.Values[itemFieldStackCount] = 20
	herb.Values[itemFieldFlags] = 0x1

	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{
		selfGUID: self, 5001: potion, 5002: herb,
	}}
	got := Read(src)

	if got.Money != 1234 {
		t.Errorf("Money = %d, want 1234", got.Money)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2 (%+v)", len(got.Items), got.Items)
	}
	first, second := got.Items[0], got.Items[1]
	if first.Slot != (SlotRef{Container: ContainerBackpack, Slot: 0}) {
		t.Errorf("first slot = %v, want backpack[0]", first.Slot)
	}
	if first.Entry != 118 || first.Count != 5 || !first.Known {
		t.Errorf("first item = %+v, want entry 118 count 5 known", first)
	}
	if second.Entry != 2447 || second.Count != 20 || second.Flags != 0x1 {
		t.Errorf("second item = %+v, want entry 2447 count 20 flags 1", second)
	}
	if free := got.FreeBagSlots(); free != backpackSlots-2 {
		t.Errorf("FreeBagSlots = %d, want %d", free, backpackSlots-2)
	}
	if got.Count(2447) != 20 {
		t.Errorf("Count(2447) = %d, want 20", got.Count(2447))
	}
}

// TestRead_BagContents covers the two-step lookup: the equipped bag GUID lives
// in PLAYER_FIELD_INV_SLOT_HEAD slot 19, its contents on the bag object itself.
func TestRead_BagContents(t *testing.T) {
	self := newObj(selfGUID, 0, client.ObjectTypePlayer)
	putGUID(self, playerFieldInvSlotHead+uint16(equipmentSlots)*2, 6001) // bag slot 0

	bag := newObj(6001, 4496, client.ObjectTypeContainer)
	bag.Values[containerFieldNumSlots] = 6
	putGUID(bag, containerFieldSlot1+0*2, 6002)
	putGUID(bag, containerFieldSlot1+2*2, 6003)
	putGUID(bag, containerFieldSlot1+5*2, 0) // empty slot stays empty

	ore := newObj(6002, 2770, client.ObjectTypeItem)
	ore.Values[itemFieldStackCount] = 3
	gem := newObj(6003, 774, client.ObjectTypeItem)

	src := fakeSource{self: selfGUID, objs: map[uint64]*client.WorldObject{
		selfGUID: self, 6001: bag, 6002: ore, 6003: gem,
	}}
	got := Read(src)

	if len(got.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3 (bag itself plus two contents): %+v", len(got.Items), got.Items)
	}
	if got.Items[0].Slot != (SlotRef{Container: ContainerBag, Bag: 0}) {
		t.Errorf("bag slot = %v, want bag0", got.Items[0].Slot)
	}
	content := got.Items[2]
	if content.Slot != (SlotRef{Container: ContainerBag, Bag: 0, Slot: 2}) {
		t.Errorf("content slot = %v, want bag0[2]", content.Slot)
	}
	if content.Entry != 774 || !content.Known {
		t.Errorf("content = %+v, want entry 774 known", content)
	}
	if got.Count(2770) != 3 {
		t.Errorf("Count(2770) = %d, want 3", got.Count(2770))
	}

	// The backpack is empty and the two items of the 6-slot bag count against it.
	if len(got.Containers) != 2 {
		t.Fatalf("len(Containers) = %d, want 2: %+v", len(got.Containers), got.Containers)
	}
	if c := got.Containers[1]; c.Capacity != 6 || c.Used != 2 {
		t.Errorf("bag container = %+v, want capacity 6 used 2", c)
	}
	if free := got.FreeBagSlots(); free != backpackSlots+6-2 {
		t.Errorf("FreeBagSlots = %d, want %d", free, backpackSlots+6-2)
	}
}
