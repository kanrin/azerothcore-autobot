//go:build e2e

package e2e

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// questRewardID is the quest this test hands in. Deputy Willem both starts and ends
// "Brotherhood of Thieves" (quest 18), which makes it the one quest a bot standing at the
// human starting position can hand in without walking anywhere.
const questRewardID = 18

// The collect half of that quest, from quest_template: RequiredItemId1 = 752 and
// RequiredItemCount1 = 8. `.quest complete` only marks the kill objectives, so the reward
// is still refused while these are missing — which the first version of this test found out
// by the server answering "Have you gathered those bandanas for me yet?".
const (
	questRewardItem      = 752
	questRewardItemCount = 8
)

// TestQuestRewardViaCLI drives the whole hand-in through the real binary: the world is
// seeded with a finished quest, `autobot run --features quest --until-reward` hands it in,
// and the reward is checked in the database.
//
// The GM commands are setup only — the GM level and a finished quest stand in for the
// combat this milestone does not have yet. Everything the *bot* does is its own protocol
// path: accept, complete, choose reward, and the quest leaving its log.
func TestQuestRewardViaCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	charDB := openDB(t, "characters", e2eharness.CharDSN)
	authDB := openDB(t, "auth", e2eharness.AuthDSN)
	data := openData(t, questProbePrefix())

	ensureQuestProbeAccount(t, ctx, authDB)

	// The account has to be a GM *before* it logs in: the security level is read from
	// account_access when the session is created.
	if err := e2eharness.SetGM(authDB, questAccount, 3); err != nil {
		t.Fatalf("precondition: make %s a GM: %v", questAccount, err)
	}

	guid, moneyBefore, xpBefore, err := seedFinishedQuest(t, ctx, charDB)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Logf("seeded: guid %d, quest %d is in the log and finished, money %d, xp %d",
		guid, questRewardID, moneyBefore, xpBefore)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		clearProbeGM(t, authDB)
		cleanupQuestProbe(t, cleanupCtx, data, charDB)
	})

	// Run the CLI, which is the point of this test: the flag and the whole binary path.
	out, err := runAutobotCLI(t, ctx, "--account", questAccount, "--prefix", questProbePrefix(),
		"--char-name", questCharacter, "--race", "1", "--class", "1",
		"--features", "quest", "--until-reward", strconv.FormatUint(questRewardID, 10),
		"--until-timeout", "2m")
	if err != nil {
		t.Fatalf("autobot run: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{"quest: rewarded", "quest left the log after being handed in"} {
		if !strings.Contains(out, want) {
			t.Errorf("the CLI output does not contain %q:\n%s", want, out)
		}
	}

	// The database is the authority on the reward: experience went up, money did not go
	// down, and the quest is no longer in progress.
	//
	// Experience is the signal, not money: quest 18 pays experience and an item at level 5
	// and no copper at all, which the first version of this test read as "the reward never
	// happened".
	var moneyAfter, xpAfter uint32
	if err := charDB.QueryRow(`SELECT money, xp FROM characters WHERE guid = ?`, guid).
		Scan(&moneyAfter, &xpAfter); err != nil {
		t.Fatalf("read the character after the reward: %v", err)
	}
	if xpAfter <= xpBefore {
		t.Errorf("experience = %d after the hand-in, was %d before it, want an increase",
			xpAfter, xpBefore)
	}
	if moneyAfter < moneyBefore {
		t.Errorf("money = %d after the hand-in, was %d before it, want it not to go down",
			moneyAfter, moneyBefore)
	}

	var pending int
	if err := charDB.QueryRow(
		`SELECT COUNT(*) FROM character_queststatus WHERE guid = ? AND quest = ? AND status IN (1, 3)`,
		guid, questRewardID).Scan(&pending); err != nil {
		t.Fatalf("count the quest status: %v", err)
	}
	if pending != 0 {
		t.Errorf("quest %d is still in character_queststatus as complete or in progress", questRewardID)
	}

	// The CLI's own reward line, so the run's evidence is in the test log.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "quest: rewarded") {
			t.Logf("CLI: %s", strings.TrimSpace(line))
		}
	}
	t.Logf("rewarded: xp %d -> %d, money %d -> %d, quest %d is no longer pending",
		xpBefore, xpAfter, moneyBefore, moneyAfter, questRewardID)
}
