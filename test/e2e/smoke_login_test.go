//go:build e2e

// Package e2e holds the live-stack tests. They need a running AzerothCore
// (authserver + worldserver + MySQL) and are therefore excluded from the
// offline build by the e2e tag; the offline `go test ./...` must skip them.
//
// Connection settings come from the same env vars upstream's harness uses:
//
//	E2E_AUTH_ADDR, E2E_AUTH_DSN, E2E_CHAR_DSN, E2E_WORLD_DSN, E2E_WORLD_LOG
//
// Run one case:
//
//	set -a; source .env.local; set +a
//	go test -tags=e2e ./test/e2e/... -run TestSmokeLogin -count=1 -v -timeout 10m
package e2e

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"azerothcore-autobot/internal/wow"

	"github.com/azerothcore/AzerothGhost/client"
	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"

	// e2eharness only imports database/sql; the driver has to be registered by
	// the caller, which is what upstream's own e2e suite does too.
	_ "github.com/go-sql-driver/mysql"
)

// The smoke test owns exactly one account and one character, both under the
// project's bot prefix. Names are fixed rather than randomised so that a rerun
// proves the idempotency path instead of creating a second character.
//
// Leftover state: this test deliberately does NOT delete the character/account
// it creates — deleting a character is the milestone-1 cleanup path
// (CMSG_CHAR_DELETE) owned by internal/wowdata and verified from the CLI. Until
// then the bot account stays in the auth database, which is harmless on a
// server that had zero accounts before.
const (
	// smokePrefix is the bot prefix the data layer's guard enforces: every name
	// it is allowed to touch starts with it.
	smokePrefix = "autobot"

	smokeAccount  = "autobotprobe"
	smokePassword = "autobotpass"
	smokeCharName = "Autobotprobe"
)

// TestSmokeLogin proves milestone 1 end to end on a live stack: provision the
// account out of band, then let internal/wow do the whole session handshake
// (SRP6 -> world socket -> PhaseAuthed -> char list -> [create] -> login) and
// leave the session in PhaseInWorld.
//
// The second subtest re-runs the same handshake and asserts that the character
// is reused rather than duplicated, which is what makes repeated `autobot run`
// invocations safe.
func TestSmokeLogin(t *testing.T) {
	authDB := openDB(t, "auth", e2eharness.AuthDSN)
	charDB := openDB(t, "characters", e2eharness.CharDSN)

	// Precondition, not a soft pass: the harness account must be creatable and
	// nothing else may own the character name we are about to use.
	requireCleanSlot(t, charDB)

	if err := e2eharness.EnsureAccount(authDB, smokeAccount, smokePassword); err != nil {
		t.Fatalf("precondition: provision account %s in %s: %v", smokeAccount, e2eharness.AuthDSN, err)
	}
	t.Logf("provisioned account %s (idempotent)", strings.ToUpper(smokeAccount))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first := enterWorld(t, ctx)
	guid := first.GUID
	if guid == 0 {
		t.Fatalf("EnterWorld returned guid 0")
	}

	t.Run("character row matches what we asked for", func(t *testing.T) {
		var race, class, level uint8
		err := charDB.QueryRow(
			`SELECT race, class, level FROM characters WHERE guid=?`, guid).Scan(&race, &class, &level)
		if err != nil {
			t.Fatalf("read character %d from characters db: %v", guid, err)
		}
		if race != 1 || class != 1 {
			t.Errorf("character race/class = %d/%d, want 1/1 (human warrior)", race, class)
		}
		if level == 0 {
			t.Errorf("character level = 0, want >= 1")
		}
		if n := countCharactersNamed(t, charDB, smokeCharName); n != 1 {
			t.Errorf("characters named %s = %d, want 1", smokeCharName, n)
		}
		t.Logf("character %d: race=%d class=%d level=%d", guid, race, class, level)
	})

	t.Run("second login reuses the same character", func(t *testing.T) {
		second := enterWorld(t, ctx)
		if second.GUID != guid {
			t.Errorf("second login guid = %d, want %d (must not create a duplicate)", second.GUID, guid)
		}
		if n := countCharactersNamed(t, charDB, smokeCharName); n != 1 {
			t.Errorf("characters named %s after relog = %d, want 1", smokeCharName, n)
		}
	})
}

// enterWorld opens a fresh session, drives the character-select handshake and
// asserts the session really ended up in the world. Every session it opens is
// closed before returning, so the next call starts from a clean socket (an
// abandoned socket would keep authenticating with a stale session key).
func enterWorld(t *testing.T, ctx context.Context) wow.Character {
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
	defer sess.Close()

	// Race drives GM-command chat language; set it for parity with the harness
	// even though this test sends no chat.
	sess.SetCharRace(1)

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

	if got := sess.Phase(); got != client.PhaseInWorld {
		t.Fatalf("session phase = %s, want %s", got, client.PhaseInWorld)
	}
	if !sess.InWorld() {
		t.Fatalf("InWorld() = false although phase is %s", sess.Phase())
	}
	if sess.CharGUID() != char.GUID {
		t.Fatalf("Session.CharGUID() = %d, want %d returned by EnterWorld", sess.CharGUID(), char.GUID)
	}

	x, y, z, o, mapID := sess.Position()
	t.Logf("in world: char=%s guid=%d race=%d class=%d level=%d map=%d pos=(%.2f, %.2f, %.2f, o=%.2f) hp=%d/%d",
		char.Name, char.GUID, char.Race, char.Class, char.Level, mapID, x, y, z, o, sess.Health(), sess.MaxHealth())
	return char
}

// openDB opens one of the three databases, reporting the DSN on failure so an
// unreachable stack shows up as a precondition instead of a mystery.
func openDB(t *testing.T, what, dsn string) *sql.DB {
	t.Helper()

	var (
		db  *sql.DB
		err error
	)
	switch what {
	case "auth":
		db, err = e2eharness.OpenAuthDB()
	case "characters":
		db, err = e2eharness.OpenCharDB()
	case "world":
		db, err = e2eharness.OpenWorldDB()
	default:
		t.Fatalf("openDB: unknown database %q", what)
	}
	if err != nil {
		t.Fatalf("precondition: cannot open %s database (%s): %v - is the server stack running?", what, dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// requireCleanSlot makes sure the character name we are about to use cannot
// collide with something that is not ours: e2eharness.CleanupPlayer does not
// delete characters, so a leftover row would silently be reused and the test
// would pass while proving nothing.
func requireCleanSlot(t *testing.T, charDB *sql.DB) {
	t.Helper()

	var (
		guid   uint64
		level  uint8
		online uint8
	)
	err := charDB.QueryRow(
		`SELECT guid, level, online FROM characters WHERE name=?`, smokeCharName).Scan(&guid, &level, &online)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return // the usual case on a fresh server: nothing to collide with
	case err != nil:
		t.Fatalf("precondition: query characters by name: %v", err)
	}

	if online != 0 {
		t.Fatalf("precondition: character %s (guid=%d) is already online - another session owns it", smokeCharName, guid)
	}
	t.Logf("reusing existing character %s guid=%d level=%d (idempotent rerun)", smokeCharName, guid, level)
}

// countCharactersNamed returns how many rows carry the character name.
func countCharactersNamed(t *testing.T, charDB *sql.DB, name string) int {
	t.Helper()

	var n int
	if err := charDB.QueryRow(`SELECT COUNT(*) FROM characters WHERE name=?`, name).Scan(&n); err != nil {
		t.Fatalf("count characters named %s: %v", name, err)
	}
	return n
}
