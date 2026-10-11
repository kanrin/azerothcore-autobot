//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// TestCleanupRemovesBotData is the counterpart of TestSmokeLogin: it proves the
// project can take its data away again, which the project rules require of every
// write it makes.
//
// It depends on the smoke test having provisioned the account and character, so
// it reports a missing precondition instead of passing on an empty database.
func TestCleanupRemovesBotData(t *testing.T) {
	charDB := openDB(t, "characters", e2eharness.CharDSN)
	authDB := openDB(t, "auth", e2eharness.AuthDSN)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var guid uint64
	err := charDB.QueryRow(`SELECT guid FROM characters WHERE name = ?`, smokeCharName).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("precondition: character %s does not exist - run TestSmokeLogin first", smokeCharName)
	}
	if err != nil {
		t.Fatalf("precondition: look up %s: %v", smokeCharName, err)
	}

	data, err := wowdata.Open(wowdata.Options{
		AuthDSN:  e2eharness.AuthDSN,
		CharDSN:  e2eharness.CharDSN,
		WorldDSN: e2eharness.WorldDSN,
		Prefix:   "autobot",
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("open data layer: %v", err)
	}
	defer func() { _ = data.Close() }()

	// 1. The account may not go while it still owns a character: deleting it
	//    would leave that row behind, to reappear under the next account
	//    provisioned with the same name.
	if err := data.DeleteAccount(ctx, smokeAccount); !errors.Is(err, wowdata.ErrAccountNotEmpty) {
		t.Fatalf("DeleteAccount with a character present = %v, want ErrAccountNotEmpty", err)
	}
	t.Logf("refused to delete %s while it still owns %s (expected)", smokeAccount, smokeCharName)

	// 2. Delete the character through the protocol: the server owns the row
	//    cleanup and CMSG_CHAR_DELETE is the only supported way to ask for it.
	sess, err := wow.Dial(ctx, wow.ConnectOptions{
		AuthAddr: e2eharness.AuthAddr,
		Username: smokeAccount,
		Password: smokePassword,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()

	if err := sess.DeleteCharacter(ctx, guid, 30*time.Second); err != nil {
		t.Fatalf("DeleteCharacter(%d): %v", guid, err)
	}
	waitCharacterGone(t, charDB, guid, 15*time.Second)

	// 3. Now the account can go as well.
	if err := data.DeleteAccount(ctx, smokeAccount); err != nil {
		t.Fatalf("DeleteAccount after the character was deleted: %v", err)
	}

	var accounts int
	err = authDB.QueryRow(`SELECT COUNT(*) FROM account WHERE username = ?`, "AUTOBOTPROBE").Scan(&accounts)
	if err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 0 {
		t.Errorf("account rows for AUTOBOTPROBE = %d, want 0", accounts)
	}
	if n := countCharactersNamed(t, charDB, smokeCharName); n != 0 {
		t.Errorf("characters named %s = %d, want 0", smokeCharName, n)
	}
	t.Logf("cleanup complete: character %d and account %s are gone", guid, smokeAccount)
}

// waitCharacterGone polls the database until the character row is gone.
//
// The database is the authority, and SMSG_CHAR_DELETE could in principle arrive
// before the row is committed, so the packet alone is not proof. Polling a real
// signal is the sanctioned pattern; a bare sleep would not be.
func waitCharacterGone(t *testing.T, charDB *sql.DB, guid uint64, timeout time.Duration) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		var n int
		if err := charDB.QueryRow(`SELECT COUNT(*) FROM characters WHERE guid = ?`, guid).Scan(&n); err != nil {
			t.Fatalf("count character %d: %v", guid, err)
		}
		if n == 0 {
			return
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("character %d is still in the database %s after SMSG_CHAR_DELETE", guid, timeout)
		}
	}
}
