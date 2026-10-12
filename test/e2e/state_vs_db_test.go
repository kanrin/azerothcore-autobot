//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/state"
	"azerothcore-autobot/internal/wowdata"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// TestStateMatchesDatabase is the live acceptance test for internal/wow/state.
//
// Every offset that package reads comes from AzerothCore's update-field layout,
// and a wrong offset does not fail — it returns a plausible wrong number. So the
// assertion is not "the snapshot is not empty" but "the snapshot equals what the
// characters database says about the same character": money, every occupied
// inventory slot (entry, stack count, durability) and every skill.
//
// The account is provisioned here and the character is created on demand, so the
// test stands on its own rather than depending on TestSmokeLogin having run.
func TestStateMatchesDatabase(t *testing.T) {
	charDB := openDB(t, "characters", e2eharness.CharDSN)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ensureSmokeAccount(t, ctx)
	guid := lookupCharacterByName(t, charDB, smokeCharName)
	sess, char := openSmokeSession(t, ctx)
	if char.GUID != guid {
		t.Fatalf("logged in as guid %d, but %s is guid %d in the database", char.GUID, smokeCharName, guid)
	}

	snap := state.Read(sess)
	t.Logf("snapshot: %s", snap)

	// A slot whose item object is not in the cache is reported as unresolved
	// rather than dropped. Right after login there should be none: otherwise the
	// comparisons below would run against a half-read inventory and could pass
	// for the wrong reason.
	if unknown := snap.UnknownEntries(); len(unknown) != 0 {
		t.Fatalf("occupied slots whose item object is missing from the cache = %d (%v); the snapshot is incomplete",
			len(unknown), unknown)
	}

	t.Run("money matches the characters table", func(t *testing.T) {
		var money uint32
		if err := charDB.QueryRow(`SELECT money FROM characters WHERE guid=?`, guid).Scan(&money); err != nil {
			t.Fatalf("read money of character %d: %v", guid, err)
		}
		if snap.Money != money {
			t.Errorf("PLAYER_FIELD_COINAGE reads %d copper, the database says %d", snap.Money, money)
		}
	})

	t.Run("every inventory slot matches item_instance", func(t *testing.T) {
		want := dbInventory(t, charDB, guid)
		got := make(map[slotKey]state.Item, len(snap.Items))
		for _, item := range snap.Items {
			got[itemKey(item.Slot)] = item
		}

		if len(got) != len(want) {
			t.Errorf("snapshot has %d occupied slot(s), the database has %d\nsnapshot: %v\ndatabase: %v",
				len(got), len(want), keysOf(got), keysOf(want))
		}
		// Check both directions. Equal counts are not enough: a slot mapped to
		// the wrong key hides behind a matching count, which is exactly how the
		// backpack offset bug got past the first version of this test.
		for key, gotItem := range got {
			if _, ok := want[key]; !ok {
				t.Errorf("snapshot has an item in %s (entry %d) that the database does not have",
					key, gotItem.Entry)
			}
		}
		for key, dbItem := range want {
			gotItem, ok := got[key]
			if !ok {
				t.Errorf("database row in %s (entry %d) is missing from the snapshot", key, dbItem.Entry)
				continue
			}
			if gotItem.Entry != dbItem.Entry {
				t.Errorf("%s entry = %d, the database says %d", key, gotItem.Entry, dbItem.Entry)
			}
			// 3.3.5a stores a single item as a stack count of 0, and so does the
			// database; compare the raw field, not the "at least one" reading.
			if gotItem.Count != dbItem.Count {
				t.Errorf("%s stack count = %d, the database says %d", key, gotItem.Count, dbItem.Count)
			}
			if gotItem.Durability != dbItem.Durability {
				t.Errorf("%s durability = %d, the database says %d", key, gotItem.Durability, dbItem.Durability)
			}
		}
	})

	t.Run("every skill matches character_skills", func(t *testing.T) {
		want := dbSkills(t, charDB, guid)
		got := make(map[uint16]state.Skill, len(snap.Skills))
		for _, skill := range snap.Skills {
			got[skill.Line] = skill
		}
		if len(got) != len(want) {
			t.Errorf("snapshot has %d skill(s), the database has %d\nsnapshot: %v\ndatabase: %v",
				len(got), len(want), skillLinesOf(got), skillLinesOf(want))
		}
		for line, dbSkill := range want {
			gotSkill, ok := got[line]
			if !ok {
				t.Errorf("skill %d (value %d/%d) is missing from the snapshot", line, dbSkill.Value, dbSkill.Max)
				continue
			}
			if gotSkill.Value != dbSkill.Value || gotSkill.Max != dbSkill.Max {
				t.Errorf("skill %d = %d/%d, the database says %d/%d",
					line, gotSkill.Value, gotSkill.Max, dbSkill.Value, dbSkill.Max)
			}
		}
	})

	if err := sess.Logout(ctx, 0); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

// ensureSmokeAccount provisions the smoke account, which is the precondition for
// dialling at all. It is idempotent, so it can run before every test that needs
// the account without caring about what ran first.
func ensureSmokeAccount(t *testing.T, ctx context.Context) {
	t.Helper()

	data, err := wowdata.Open(wowdata.Options{
		AuthDSN:  e2eharness.AuthDSN,
		CharDSN:  e2eharness.CharDSN,
		WorldDSN: e2eharness.WorldDSN,
		Prefix:   smokePrefix,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("precondition: open data layer: %v", err)
	}
	defer func() { _ = data.Close() }()

	if _, err := data.EnsureAccount(ctx, smokeAccount, smokePassword); err != nil {
		t.Fatalf("precondition: provision account %s: %v", smokeAccount, err)
	}
}

// openSmokeSession dials the smoke account and enters the world as the smoke
// character. The race and class only matter when the character has to be
// created, and they match TestSmokeLogin so both tests describe the same bot.
func openSmokeSession(t *testing.T, ctx context.Context) (*wow.Session, wow.Character) {
	t.Helper()

	sess, err := wow.Dial(ctx, wow.ConnectOptions{
		AuthAddr: e2eharness.AuthAddr,
		Username: smokeAccount,
		Password: smokePassword,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("Dial (auth %s): %v", e2eharness.AuthAddr, err)
	}
	t.Cleanup(sess.Close)

	char, err := sess.EnterWorld(ctx, wow.EnterWorldOptions{
		Name:       smokeCharName,
		Race:       1,
		Class:      1,
		Appearance: wow.DefaultAppearance(0),
		Logf:       t.Logf,
	})
	if err != nil {
		t.Fatalf("EnterWorld (character %s): %v", smokeCharName, err)
	}
	return sess, char
}
