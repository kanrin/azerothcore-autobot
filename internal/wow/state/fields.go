// Package state derives the bot's own character state — money, inventory and
// skills — from the object cache AzerothGhost maintains for the logged-in
// character.
//
// Why this package exists: AzerothGhost has typed getters for health, level,
// auras and equipped-item entries, but no inventory, skill or money model, and
// every vendor, gathering, profession and auction feature needs one. The values
// are read through WorldObject.Value / WorldObject.GUIDField, which the client
// documents as thread-safe, so no locking is needed here.
//
// Every offset below is AzerothCore's update-field layout, quoted with the line
// it comes from. Do not move a constant without re-checking that file: reading a
// neighbouring field does not fail, it silently returns a plausible wrong number.
//
// Source: azerothcore-wotlk/src/server/game/Entities/Object/Updates/UpdateFields.h
// and src/server/game/Entities/Player/Player.h.
package state

// Object field boundaries, in the server's own arithmetic.
const (
	// UpdateFields.h:30 OBJECT_END = 0x0006.
	objectEnd = 0x0006

	// UpdateFields.h:73 ITEM_END = OBJECT_END + 0x003A.
	itemEnd = objectEnd + 0x003A

	// UpdateFields.h:175 UNIT_END = OBJECT_END + 0x008E.
	unitEnd = objectEnd + 0x008E
)

// Player update fields read by this package.
const (
	// UpdateFields.h:327 — Size 46, i.e. 23 GUIDs: slots 0..18 are equipment and
	// 19..22 the equipped bags (Player.h:686-687 INVENTORY_SLOT_BAG_START/END).
	playerFieldInvSlotHead = unitEnd + 0x00B0

	// UpdateFields.h:328 — Size 32, i.e. 16 GUIDs: the main backpack
	// (Player.h:692-693 INVENTORY_SLOT_ITEM_START/END = 23/39).
	playerFieldPackSlot1 = unitEnd + 0x00DE

	// UpdateFields.h:329 — Size 56, i.e. 28 GUIDs: the bank.
	playerFieldBankSlot1 = unitEnd + 0x00FE

	// UpdateFields.h:330 — Size 14, i.e. 7 GUIDs: the bank bags.
	playerFieldBankBagSlot1 = unitEnd + 0x0136

	// UpdateFields.h:341 — Size 384: 127 skills of three fields each
	// (Player.h:71 PLAYER_MAX_SKILLS = 127; Player.h:79-89 is the packing).
	playerSkillInfo11 = unitEnd + 0x01E8

	// UpdateFields.h:359 — Size 1: copper, the same value GetMoney() returns.
	playerFieldCoinage = unitEnd + 0x03FE
)

// Item update fields (UpdateFields.h:35-70). A GUID owns two consecutive fields,
// so GUID indices go through GUIDField and scalars through Value.
const (
	itemFieldStackCount    = objectEnd + 0x0008 // :39
	itemFieldFlags         = objectEnd + 0x000F // :42
	itemFieldDurability    = objectEnd + 0x0036 // :69
	itemFieldMaxDurability = objectEnd + 0x0037 // :70
)

// Container update fields (UpdateFields.h:78-80), present on bag items only.
const (
	containerFieldNumSlots = itemEnd + 0x0000 // :78 — Size 1
	containerFieldSlot1    = itemEnd + 0x0002 // :80 — Size 72, i.e. 36 GUIDs
)

// Slot counts, derived from the field sizes above and the Player.h enums. They
// are the loop bounds of Read.
const (
	equipmentSlots    = 19 // Player.h equipment slots 0..18
	bagSlots          = 4  // Player.h:686-687, INVENTORY_SLOT_BAG_START..END
	backpackSlots     = 16 // Player.h:692-693, ITEM_START..END = 23..38
	bankSlots         = 28 // PLAYER_FIELD_BANK_SLOT_1 covers 28 GUIDs
	bankBagSlots      = 7  // PLAYER_FIELD_BANKBAG_SLOT_1 covers 7 GUIDs
	maxSkills         = 127
	maxContainerSlots = 36 // CONTAINER_FIELD_SLOT_1 covers 36 GUIDs
)

// skillPack splits one packed PLAYER_SKILL_INFO_1_1 field into its two 16-bit
// halves. AzerothCore builds these fields with MAKE_PAIR32 (Player.h:79-89).
func lowHalf(v uint32) uint16  { return uint16(v & 0xFFFF) }
func highHalf(v uint32) uint16 { return uint16(v >> 16) }
