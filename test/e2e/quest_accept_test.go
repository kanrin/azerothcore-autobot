//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"azerothcore-autobot/internal/feature/quest"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/state"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// TestQuestAcceptPutsItInTheLog is the acceptance test for taking a quest: the bot
// opens the dialog with Deputy Willem, picks what the world says it can take, accepts
// it, and both the client's quest log and the character's database row agree.
//
// It uses the same calls `autobot run --features quest` does, so the CLI is a thin
// wrapper over what is verified here. No pathfinding is involved: Willem stands 2.3
// yards from the human starting position.
func TestQuestAcceptPutsItInTheLog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	charDB := openDB(t, "characters", e2eharness.CharDSN)
	authDB := openDB(t, "auth", e2eharness.AuthDSN)
	data := openData(t, questProbePrefix())

	ensureQuestProbeAccount(t, ctx, authDB)
	sess, guid := loginQuestProbe(t, ctx)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cleanupQuestProbe(t, cleanupCtx, data, charDB)
	})
	defer sess.Close()

	npc := waitForUnitEntry(t, sess, willemEntry, 5, 20*time.Second)

	// 1. Open the dialog. The answer may be a quest list, one quest's details or a
	//    gossip menu; the test records which, because that is server data.
	question, err := sess.QuestGiverHello(ctx, npc, 15*time.Second)
	if err != nil {
		t.Fatalf("questgiver hello: %v", err)
	}
	t.Logf("Willem answered with %s and %d quest(s): %s",
		wow.OpcodeName(question.AnsweredWith), len(question.Quests), question.Greeting)
	if len(question.Quests) == 0 {
		t.Fatalf("Willem offered no quests to %s", questCharacter)
	}

	// 2. The picker decides from the world knowledge, exactly as the behaviour does.
	ids := make([]uint32, 0, len(question.Quests))
	for _, entry := range question.Quests {
		ids = append(ids, entry.ID)
	}
	templates, err := data.QuestsByIDs(ctx, ids)
	if err != nil {
		t.Fatalf("read quest templates: %v", err)
	}

	candidates := make([]quest.Candidate, 0, len(ids))
	for _, entry := range question.Quests {
		candidate := quest.Candidate{ID: entry.ID, Title: entry.Title, Level: entry.Level}
		if template, found := templates[entry.ID]; found {
			candidate.Title = template.Title
			candidate.Level = template.Level
			candidate.MinLevel = template.MinLevel
		}
		candidates = append(candidates, candidate)
	}

	pick, ok := quest.ChooseAcceptable(candidates, 1) // the probe character is level 1
	if !ok {
		for _, skip := range quest.Explain(candidates, 1) {
			t.Logf("skipped %d %q: %s", skip.Candidate.ID, skip.Candidate.Title, skip.Reason)
		}
		t.Fatal("nothing in Willem's list is takeable at level 1")
	}
	t.Logf("picking quest %d %q (level %d, needs level %d)",
		pick.ID, pick.Title, pick.Level, pick.MinLevel)

	// 3. The details dialog, which is also the check that the NPC really has it.
	details, err := sess.QueryQuest(ctx, npc, pick.ID, 15*time.Second)
	if err != nil {
		t.Fatalf("ask for the details of %d: %v", pick.ID, err)
	}
	if details.QuestID != pick.ID {
		t.Fatalf("details are for quest %d, asked about %d", details.QuestID, pick.ID)
	}
	if details.Title != pick.Title {
		t.Errorf("details title = %q, world says %q", details.Title, pick.Title)
	}
	t.Logf("details: %q, objectives %q, auto-accept %v", details.Title, details.Objectives, details.AutoAccept)

	// 4. Accept, and wait for the confirmation: the quest appearing in the log.
	if err := sess.AcceptQuest(ctx, npc, pick.ID, 20*time.Second); err != nil {
		t.Fatalf("accept quest %d: %v", pick.ID, err)
	}
	slot, found := state.Read(sess).Quest(pick.ID)
	if !found {
		t.Fatalf("quest %d is not in the quest log after accepting it", pick.ID)
	}
	t.Logf("quest %d is in log slot %d, state %#x (complete: %v), counters %v",
		slot.QuestID, slot.Index, slot.State, slot.IsComplete(), slot.Counters)

	// 5. Log out, and only then read the database.
	//
	// The server writes character_queststatus when it saves the character, and a
	// logout is what forces that save: checking before it fails on a quest that is
	// genuinely accepted, which is exactly what the first version of this test did.
	if err := sess.Logout(ctx, 40*time.Second); err != nil {
		t.Fatalf("logout before reading the database: %v", err)
	}
	waitQuestStatusRow(t, charDB, guid, pick.ID, 30*time.Second)
}

// waitQuestStatusRow polls character_queststatus until the row appears.
//
// The client's quest log is a cache of update fields, and the server writes the row
// when it saves the character; polling the real signal beats sleeping and hoping.
func waitQuestStatusRow(t *testing.T, charDB *sql.DB, guid uint64, questID uint32, timeout time.Duration) {
	t.Helper()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		var status uint8
		err := charDB.QueryRow(
			`SELECT status FROM character_queststatus WHERE guid = ? AND quest = ?`,
			guid, questID).Scan(&status)
		switch {
		case err == nil:
			// QuestDef.h:98-108 — 1 is COMPLETE, 3 INCOMPLETE, 5 FAILED.
			if status != 1 && status != 3 {
				t.Errorf("quest %d has status %d in the database, want 1 (complete) or 3 (incomplete)",
					questID, status)
			}
			t.Logf("character_queststatus: guid=%d quest=%d status=%d", guid, questID, status)
			return
		case err != sql.ErrNoRows:
			t.Fatalf("read character_queststatus: %v", err)
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("quest %d is in the client's log but not in character_queststatus after %s",
				questID, timeout)
		}
	}
}
