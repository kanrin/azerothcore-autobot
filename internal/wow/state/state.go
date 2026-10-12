package state

import (
	"fmt"

	"github.com/azerothcore/AzerothGhost/client"
)

// Source is the slice of the world client this package reads. *wow.Session
// satisfies it structurally (CharGUID + Object), so state never imports the
// session package and a fake Source can drive the unit tests.
//
// Every method must be safe to call from the agent goroutine: the object cache
// is written by the client's read goroutine, and WorldObject.Value/GUIDField are
// the documented thread-safe accessors.
type Source interface {
	// CharGUID returns the logged-in character's GUID, or 0 before login.
	CharGUID() uint64

	// Object returns the tracked object with that GUID, or nil when it is not in
	// the cache. Only the self object and the bag items are needed.
	Object(guid uint64) *client.WorldObject
}

// Container names the container a slot lives in.
type Container uint8

// Containers, in the order Read walks them.
const (
	ContainerEquipped Container = iota // equipment slots 0..18
	ContainerBag                       // an equipped bag, Bag says which (0..3)
	ContainerBackpack                  // the 16 slots of the main backpack
	ContainerBank                      // 28 bank slots
	ContainerBankBag                   // an equipped bank bag (0..6)
)

// String renders the container for logs and the CLI's state dump.
func (c Container) String() string {
	switch c {
	case ContainerEquipped:
		return "equipped"
	case ContainerBag:
		return "bag"
	case ContainerBackpack:
		return "backpack"
	case ContainerBank:
		return "bank"
	case ContainerBankBag:
		return "bankbag"
	default:
		return fmt.Sprintf("container(%d)", uint8(c))
	}
}

// SlotRef addresses one inventory slot.
type SlotRef struct {
	Container Container

	// Bag is the bag slot index for ContainerBag (0..3) and ContainerBankBag
	// (0..6); it is unused for the other containers.
	Bag uint8

	// Slot is the index inside the container.
	Slot uint8
}

// String renders the reference as "backpack[3]" or "bag2[7]".
func (r SlotRef) String() string {
	switch r.Container {
	case ContainerBag, ContainerBankBag:
		return fmt.Sprintf("%s%d[%d]", r.Container, r.Bag, r.Slot)
	default:
		return fmt.Sprintf("%s[%d]", r.Container, r.Slot)
	}
}

// Item is one occupied inventory slot.
//
// Known reports whether the item object was in the client's cache when the
// snapshot was taken. When it is false the slot is occupied but its fields have
// not arrived yet (a freshly created item, or a cache cleared by a teleport), so
// Entry/Count/durability are zero. Callers must not read a zero entry as "empty
// slot": an empty slot produces no Item at all.
type Item struct {
	Slot SlotRef

	GUID  uint64
	Entry uint32

	Count         uint32
	Durability    uint32
	MaxDurability uint32

	// Flags is ITEM_FIELD_FLAGS, which carries the binding, locked and wrapped
	// bits. Kept raw; interpret with the constants in
	// azerothcore-wotlk/src/server/game/Entities/Item/Item.h.
	Flags uint32

	Known bool
}

// ContainerInfo reports how full one container is. Capacity comes from
// CONTAINER_FIELD_NUM_SLOTS for bags and from the field layout for the backpack,
// so FreeBagSlots never has to guess.
type ContainerInfo struct {
	Slot     SlotRef // Container and Bag are set; Slot is unused
	Capacity uint8
	Used     uint8
}

// Snapshot is the bot's own state at one instant. It is immutable once built, so
// it can be read from any goroutine and published on the agent's blackboard.
type Snapshot struct {
	// Money is copper, the value of PLAYER_FIELD_COINAGE.
	Money uint32

	// Items holds one entry per occupied slot, in container order.
	Items []Item

	// Skills holds every non-empty PLAYER_SKILL_INFO_1_1 entry, in slot order.
	Skills []Skill

	// Level is the character's own level, UNIT_FIELD_LEVEL. Features weigh
	// requirements against it (a quest's MinLevel, a trainer's spells), so it is part
	// of the snapshot rather than a login-time value.
	Level uint8

	// Containers reports the backpack and the equipped bags only: the bank is
	// not part of "can I keep looting".
	Containers []ContainerInfo

	// Quests is the quest log, read from the player's own update fields rather
	// than from a packet — see questlog.go for why.
	Quests []QuestSlot
}
