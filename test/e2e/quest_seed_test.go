//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/state"
)

// Fixtures for the hand-in test: the world has to be seeded with a finished quest, which
// needs a GM account and two commands that stand in for combat.

// seedFinishedQuest logs the probe character in as a GM, levels it to where quest 18 can
// be taken, accepts that quest at Willem through the normal protocol path, marks its
// objectives done, and logs out again. It returns the character guid and its money after
// the logout (when the server has saved it).
//
// The two GM commands are setup: the level because the quest's MinLevel is 2, and
// `.quest complete` because killing ten Defias Thugs is combat, which this milestone does
// not have. Each is followed by waiting for its observable effect — the level in the
// character's own object, the complete flag in the quest log — rather than a sleep.
func seedFinishedQuest(t *testing.T, ctx context.Context, charDB *sql.DB) (guid uint64, money, xp uint32, err error) {
	t.Helper()

	sess, guid := loginQuestProbe(t, ctx)
	defer sess.Close()

	if err := sess.Client().SendGMCommand(".gm on"); err != nil {
		return 0, 0, 0, fmt.Errorf("send .gm on: %w", err)
	}
	if err := sess.Client().SendGMCommand(".character level 5"); err != nil {
		return 0, 0, 0, fmt.Errorf("send .character level 5: %w", err)
	}
	if err := waitForLevel(sess, 5, 30*time.Second); err != nil {
		return 0, 0, 0, err
	}

	// The quest goes into the log through a GM command rather than through the accept
	// path, because of a prerequisite that is a finding in itself: quest 18 requires quest
	// 783 *rewarded* (quest_template_addon.PrevQuestID = 783), and 783 is handed in to
	// Marshal McBride 56 yards away. Walking there is the pathfinding milestone's problem,
	// and accepting is covered by TestQuestAcceptPutsItInTheLog — what this test is about
	// is the hand-in.
	if err := sess.Client().SendGMCommand(fmt.Sprintf(".quest add %d", questRewardID)); err != nil {
		return 0, 0, 0, fmt.Errorf("send .quest add: %w", err)
	}
	if err := waitForQuestInLog(sess, questRewardID, 30*time.Second); err != nil {
		return 0, 0, 0, err
	}

	if err := sess.Client().SendGMCommand(fmt.Sprintf(".quest complete %d", questRewardID)); err != nil {
		return 0, 0, 0, fmt.Errorf("send .quest complete: %w", err)
	}
	if err := waitForQuestComplete(sess, questRewardID, 30*time.Second); err != nil {
		return 0, 0, 0, err
	}

	// `.quest complete` marks the kill objectives, not the collect ones: the reward is
	// still refused while the required items are missing, so the bandanas are added too and
	// the wait is on their count in the client's own snapshot.
	if err := sess.Client().SendGMCommand(
		fmt.Sprintf(".additem %d %d", questRewardItem, questRewardItemCount)); err != nil {
		return 0, 0, 0, fmt.Errorf("send .additem: %w", err)
	}
	if err := waitForItemCount(sess, questRewardItem, questRewardItemCount, 30*time.Second); err != nil {
		return 0, 0, 0, err
	}

	// Log out so the server saves the character, and read the money and experience the
	// reward has to move.
	if err := sess.Logout(ctx, 40*time.Second); err != nil {
		return 0, 0, 0, fmt.Errorf("logout after seeding: %w", err)
	}

	if err := charDB.QueryRow(`SELECT money, xp FROM characters WHERE guid = ?`, guid).
		Scan(&money, &xp); err != nil {
		return 0, 0, 0, fmt.Errorf("read money and xp of guid %d: %w", guid, err)
	}
	return guid, money, xp, nil
}

// waitForLevel waits until the character's own level reaches want.
func waitForLevel(sess *wow.Session, want uint8, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if got := state.Read(sess).Level; got >= want {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("level is %d after %s, want %d: did the GM command get through?",
				state.Read(sess).Level, timeout, want)
		}
	}
}

// waitForQuestInLog waits until the quest appears in the bot's quest log, which is how a
// GM `.quest add` becomes visible: the log is update fields, so there is no packet to
// wait for.
func waitForQuestInLog(sess *wow.Session, questID uint32, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, found := state.Read(sess).Quest(questID); found {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("quest %d did not appear in the log within %s: did .quest add land?",
				questID, timeout)
		}
	}
}

// waitForItemCount waits until the client's snapshot counts enough of an item — the same
// snapshot a feature would read before deciding anything about materials.
func waitForItemCount(sess *wow.Session, entry, want uint32, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if got := state.Read(sess).Count(entry); got >= want {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("only %d of item %d after %s, want %d: did .additem land?",
				state.Read(sess).Count(entry), entry, timeout, want)
		}
	}
}

// waitForQuestComplete waits until the quest's slot in the log reports complete — the
// same flag the hand-in decision reads.
func waitForQuestComplete(sess *wow.Session, questID uint32, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if slot, found := state.Read(sess).Quest(questID); found && slot.IsComplete() {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("quest %d is not complete in the log after %s: did .quest complete land?",
				questID, timeout)
		}
	}
}

// runAutobotCLI builds the binary and runs `autobot run` with the given flags, returning
// its combined output.
func runAutobotCLI(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()

	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "autobot")

	build := exec.Command("go", "build", "-o", bin, "./cmd/autobot")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the CLI: %v\n%s", err, out)
	}

	cmd := exec.CommandContext(ctx, bin, append([]string{"run"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AUTOBOT_PASSWORD="+questPassword)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// moduleRoot walks up from the test's working directory to the directory with go.mod, so
// the CLI can be built and run from wherever `go test` was invoked.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// clearProbeGM takes the probe account's GM level away again, so the server is left as it
// was found even if the account deletion happens to fail later.
func clearProbeGM(t *testing.T, authDB *sql.DB) {
	t.Helper()

	if _, err := authDB.Exec(
		`DELETE FROM account_access WHERE id = (SELECT id FROM account WHERE username = ?)`,
		strings.ToUpper(questAccount)); err != nil {
		t.Logf("cleanup: remove the GM level of %s: %v", questAccount, err)
	}
}
