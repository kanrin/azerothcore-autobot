//go:build e2e

package e2e

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"

	"azerothcore-autobot/internal/wow/state"
)

// slotKey identifies one inventory slot the way character_inventory does: a bag
// number plus a slot number.
type slotKey struct {
	bag  uint8
	slot uint8
}

// String renders the key the way both sides of the comparison can print it.
func (k slotKey) String() string { return fmt.Sprintf("bag%d[%d]", k.bag, k.slot) }

// itemKey translates a snapshot slot into the (bag, slot) pair
// character_inventory uses, which is the only place this test knows about that
// encoding.
//
// AzerothCore keeps the player's inventory in separate GUID arrays, chosen by
// slot number (INVENTORY_SLOT_BAG_0, i.e. bag 0 in the database, means "the
// player's own slots"):
//
//	0..18    equipment (UpdateFields.h:327, PLAYER_FIELD_INV_SLOT_HEAD)
//	19..22   equipped bags (Player.h:686-687, INVENTORY_SLOT_BAG_(START|END))
//	23..38   backpack (UpdateFields.h:328, PLAYER_FIELD_PACK_SLOT_1)
//	39..66   bank (UpdateFields.h:329, PLAYER_FIELD_BANK_SLOT_1)
//	67..73   bank bags (UpdateFields.h:330, PLAYER_FIELD_BANKBAG_SLOT_1)
//
// The contents of a bag are stored with that bag's slot number in the bag
// column, which is what the ContainerBag and ContainerBankBag cases invert. The
// backpack and bank arrays start at slot 23 and 39, so their slot index has to
// be offset — the first version of this function forgot that and reported the
// hearthstone as a database row "missing from the snapshot".
func itemKey(ref state.SlotRef) slotKey {
	switch ref.Container {
	case state.ContainerBag:
		return slotKey{bag: 19 + ref.Bag, slot: ref.Slot}
	case state.ContainerBankBag:
		return slotKey{bag: 67 + ref.Bag, slot: ref.Slot}
	case state.ContainerBackpack:
		return slotKey{bag: 0, slot: 23 + ref.Slot}
	case state.ContainerBank:
		return slotKey{bag: 0, slot: 39 + ref.Slot}
	default:
		return slotKey{bag: 0, slot: ref.Slot}
	}
}

// dbInventory reads the character's items as the database sees them.
func dbInventory(t *testing.T, charDB *sql.DB, guid uint64) map[slotKey]state.Item {
	t.Helper()

	rows, err := charDB.Query(`
		SELECT ci.bag, ci.slot, ii.itemEntry, ii.count, ii.durability
		FROM character_inventory ci
		JOIN item_instance ii ON ii.guid = ci.item
		WHERE ci.guid = ?
		ORDER BY ci.bag, ci.slot`, guid)
	if err != nil {
		t.Fatalf("read inventory of character %d: %v", guid, err)
	}
	defer func() { _ = rows.Close() }()

	items := map[slotKey]state.Item{}
	for rows.Next() {
		var (
			bag, slot    uint8
			entry, count uint32
			durability   uint32
		)
		if err := rows.Scan(&bag, &slot, &entry, &count, &durability); err != nil {
			t.Fatalf("scan inventory row: %v", err)
		}
		items[slotKey{bag: bag, slot: slot}] = state.Item{
			Entry: entry, Count: count, Durability: durability, Known: true,
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate inventory of character %d: %v", guid, err)
	}
	return items
}

// dbSkills reads the character's skills as the database sees them.
func dbSkills(t *testing.T, charDB *sql.DB, guid uint64) map[uint16]state.Skill {
	t.Helper()

	rows, err := charDB.Query(`SELECT skill, value, max FROM character_skills WHERE guid=?`, guid)
	if err != nil {
		t.Fatalf("read skills of character %d: %v", guid, err)
	}
	defer func() { _ = rows.Close() }()

	skills := map[uint16]state.Skill{}
	for rows.Next() {
		var (
			line       uint16
			value, max uint16
		)
		if err := rows.Scan(&line, &value, &max); err != nil {
			t.Fatalf("scan skill row: %v", err)
		}
		skills[line] = state.Skill{Line: line, Value: value, Max: max}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate skills of character %d: %v", guid, err)
	}
	return skills
}

// lookupCharacterByName returns the guid of a character, failing with a
// precondition rather than a mystery when the row is missing.
func lookupCharacterByName(t *testing.T, charDB *sql.DB, name string) uint64 {
	t.Helper()

	var guid uint64
	err := charDB.QueryRow(`SELECT guid FROM characters WHERE name=?`, name).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("precondition: character %s does not exist - run TestSmokeLogin first", name)
	}
	if err != nil {
		t.Fatalf("precondition: look up character %s: %v", name, err)
	}
	return guid
}

// keysOf renders the compared sets sorted, so a mismatch is readable in the test
// log instead of looking like a random order.
func keysOf(items map[slotKey]state.Item) []string {
	out := make([]string, 0, len(items))
	for key := range items {
		out = append(out, key.String())
	}
	sort.Strings(out)
	return out
}

// skillLinesOf renders the compared skill sets sorted.
func skillLinesOf[T any](skills map[uint16]T) []uint16 {
	out := make([]uint16, 0, len(skills))
	for line := range skills {
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
