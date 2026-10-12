//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"azerothcore-autobot/internal/fleet"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// fleetPrefix is deliberately different from the smoke account's prefix so the two
// never fight over rows, and short enough that the derived character names (the
// prefix's letters plus the label) stay inside AzerothCore's 12 character limit.
const (
	fleetPrefix   = "autoe2e"
	fleetPassword = "autoe2epass"
)

// TestFleetLifecycle is milestone 2's acceptance: a roster is provisioned
// (idempotently), its state is read back from the databases, all of its bots stay
// in the world at the same time, and the whole roster is taken away again.
//
// It drives the same functions `autobot fleet ...` calls, so the CLI is a thin
// wrapper over what is verified here.
//
// Rows are checked by name, never by a prefix count: a count would also match a
// leftover from an earlier run and turn a failure into a pass.
func TestFleetLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	charDB := openDB(t, "characters", e2eharness.CharDSN)

	f := writeTestFleet(t)
	data := openData(t, fleetPrefix)

	// The roster is cleaned up even when an assertion fails: leftovers would be
	// picked up by the next run and hide the real failure.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		report, _ := fleet.GC(cleanupCtx, f, data, fleetPassword, fleet.OpsOptions{Logf: t.Logf})
		for _, b := range report.Bots {
			t.Logf("cleanup: %s %s", b.Name, b.Action)
		}
	})

	// 1. The accounts are rows in the auth database.
	report, err := fleet.Provision(ctx, f, data, fleetPassword, fleet.OpsOptions{Logf: t.Logf})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if report.Failed() {
		t.Fatalf("Provision reported failures: %v", report.Err())
	}
	for _, b := range report.Bots {
		if !b.AccountCreated {
			t.Errorf("the account for %s was not created on the first run", b.Name)
		}
	}
	for _, b := range f.Bots {
		if _, found, err := data.AccountID(ctx, b.Account); err != nil || !found {
			t.Fatalf("account %s is missing after provisioning (found=%v, err=%v)", b.Account, found, err)
		}
	}

	// 2. Provisioning again must not add rows: that is what makes it safe to run
	//    before every start.
	again, err := fleet.Provision(ctx, f, data, fleetPassword, fleet.OpsOptions{Logf: t.Logf})
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	for _, b := range again.Bots {
		if b.AccountCreated {
			t.Errorf("the account for %s was created twice", b.Name)
		}
	}
	if report.Failed() {
		t.Errorf("the second provision reported failures: %v", report.Err())
	}

	// 3. Characters are created through the protocol, one bot at a time.
	report, err = fleet.Provision(ctx, f, data, fleetPassword, fleet.OpsOptions{
		Characters: true, Logf: t.Logf, Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Provision with characters: %v", err)
	}
	if report.Failed() {
		t.Fatalf("Provision with characters reported failures: %v", report.Err())
	}
	for _, b := range report.Bots {
		if !b.CharacterCreated {
			t.Errorf("the character for %s was not created", b.Name)
		}
	}

	// 4. The databases are the authority on what the roster looks like, and every
	//    character has to belong to its own bot's account.
	rows, err := fleet.Status(ctx, f, data)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for i, row := range rows {
		if !row.HasAccount || !row.HasCharacter {
			t.Errorf("%s: account=%v character=%v, want both present",
				row.Name, row.HasAccount, row.HasCharacter)
			continue
		}
		if row.Level != 1 {
			t.Errorf("%s: level = %d, want 1", row.Name, row.Level)
		}
		char, found, err := data.CharacterByName(ctx, f.Bots[i].Character)
		if err != nil || !found {
			t.Fatalf("character %s is missing (found=%v, err=%v)", f.Bots[i].Character, found, err)
		}
		if char.AccountID != row.AccountID {
			t.Errorf("%s: character %s belongs to account %d, want %d",
				row.Name, char.Name, char.AccountID, row.AccountID)
		}
	}

	split := fleet.SplitOf(f.Bots)
	if !split.Within(f.Policy.Balance.Tolerance) {
		t.Errorf("split %s is outside the tolerance %.2f", split, f.Policy.Balance.Tolerance)
	}
	t.Logf("roster: %d bots, %s, within tolerance %.2f",
		len(f.Bots), split, f.Policy.Balance.Tolerance)

	// 5. All of them online at once, which is the point of a fleet.
	outcome, err := fleet.Supervise(ctx, f, data, fleetPassword, fleet.RunOptions{
		OpsOptions: fleet.OpsOptions{Logf: t.Logf, Timeout: 2 * time.Minute},
		For:        12 * time.Second,
	})
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	if clean := outcome.Clean(); clean != len(f.Bots) {
		t.Errorf("clean bots = %d of %d, want all of them: %v", clean, len(f.Bots), outcome.Err())
	}
	for _, b := range outcome.Bots {
		if b.Logins != 1 {
			t.Errorf("%s logged in %d times, want 1", b.Name, b.Logins)
		}
		if b.Ticks < 5 {
			t.Errorf("%s ran %d ticks in %s, want the loop to have been running",
				b.Name, b.Ticks, outcome.Uptime().Round(time.Second))
		}
	}
	t.Logf("fleet ran %s: %s", outcome.Uptime().Round(time.Second), describeOutcome(outcome))

	// 6. And away again: every bot is offline first, then gone.
	for _, b := range f.Bots {
		waitCharacterOffline(t, charDB, b.Character, 30*time.Second)
	}

	report, err = fleet.GC(ctx, f, data, fleetPassword, fleet.OpsOptions{Logf: t.Logf, Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if report.Failed() {
		t.Fatalf("GC reported failures: %v", report.Err())
	}
	for _, b := range f.Bots {
		if _, found, err := data.CharacterByName(ctx, b.Character); err != nil || found {
			t.Errorf("character %s is still there after gc (found=%v, err=%v)", b.Character, found, err)
		}
		if _, found, err := data.AccountID(ctx, b.Account); err != nil || found {
			t.Errorf("account %s is still there after gc (found=%v, err=%v)", b.Account, found, err)
		}
	}

	// GC is idempotent as well: a roster that is already gone is not a failure.
	report, err = fleet.GC(ctx, f, data, fleetPassword, fleet.OpsOptions{Logf: t.Logf})
	if err != nil {
		t.Fatalf("second GC: %v", err)
	}
	if report.Failed() {
		t.Errorf("a second gc reported failures: %v", report.Err())
	}
	for _, b := range report.Bots {
		if b.Action != "absent" {
			t.Errorf("%s: action after a second gc = %q, want absent", b.Name, b.Action)
		}
	}
}

// writeTestFleet lays out a fleet.yml and a roster of three bots in a temp dir:
// two Alliance and one Horde, which is the split the balance policy is checked
// against. The host comes from the environment the live tests already export, so
// nothing about this machine ends up in a committed file.
func writeTestFleet(t *testing.T) *fleet.Fleet {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "roster"), 0o750); err != nil {
		t.Fatalf("create roster dir: %v", err)
	}

	policy := `
prefix: ` + fleetPrefix + `
auth_addr: "127.0.0.1:3724"
realm_index: 0
roster_dir: roster
login:
  stagger: 500ms
  max_concurrent: 1
  attempts: 2
balance:
  enforce: true
  tolerance: 0.34
features:
  travel: false
`
	roster := `
bots:
  - name: alpha
    race: 1
    class: 1
  - name: bravo
    race: 4
    class: 11
  - name: charlie
    race: 2
    class: 1
`
	path := filepath.Join(dir, "fleet.yml")
	if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
		t.Fatalf("write fleet.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roster", "bots.yml"), []byte(roster), 0o600); err != nil {
		t.Fatalf("write roster: %v", err)
	}

	f, err := fleet.Load(path)
	if err != nil {
		t.Fatalf("load the test fleet: %v", err)
	}
	// The live tests export E2E_AUTH_ADDR, and the fleet file keeps a placeholder
	// so no machine-specific host is written anywhere.
	if err := f.ApplyEnv(os.LookupEnv); err != nil {
		t.Fatalf("apply the environment to the test fleet: %v", err)
	}
	if len(f.Bots) != 3 {
		t.Fatalf("the test roster has %d bots, want 3", len(f.Bots))
	}
	return f
}

// describeOutcome renders the supervisor's summary for the test log.
func describeOutcome(o fleet.RunOutcome) string {
	out := ""
	for i, b := range o.Bots {
		if i > 0 {
			out += "; "
		}
		out += b.Name + " logins=" + strconv.Itoa(b.Logins) +
			" sessions=" + strconv.Itoa(b.Sessions) +
			" ticks=" + strconv.FormatInt(b.Ticks, 10) +
			" errors=" + strconv.Itoa(len(b.Errors))
	}
	return out
}
