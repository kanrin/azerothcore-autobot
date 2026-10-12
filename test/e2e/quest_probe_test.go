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

// Fixtures for the quest probe: an account of its own namespace, a human character
// (which is created at the human starting position, 2.3 yards from Deputy Willem) and
// a cleanup that takes both away again.

func ensureQuestProbeAccount(t *testing.T, ctx context.Context, authDB *sql.DB) {
	t.Helper()
	if err := e2eharness.EnsureAccount(authDB, questAccount, questPassword); err != nil {
		t.Fatalf("precondition: provision %s in %s: %v", questAccount, e2eharness.AuthDSN, err)
	}
	_ = ctx
}

// loginQuestProbe logs the probe character in and returns the session and its guid.
//
// Race 1 (human) and class 1 (warrior) are the only combination that starts next to a
// quest giver, which is what makes a quest test possible before walking works.
func loginQuestProbe(t *testing.T, ctx context.Context) (*wow.Session, uint64) {
	t.Helper()

	sess, err := wow.Dial(ctx, wow.ConnectOptions{
		AuthAddr: e2eharness.AuthAddr,
		Username: questAccount,
		Password: questPassword,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	sess.SetCharRace(1)
	char, err := sess.EnterWorld(ctx, wow.EnterWorldOptions{
		Name:       questCharacter,
		Race:       1,
		Class:      1,
		Appearance: wow.DefaultAppearance(0),
		Logf:       t.Logf,
	})
	if err != nil {
		sess.Close()
		t.Fatalf("enter world as %s: %v", questCharacter, err)
	}
	return sess, char.GUID
}

// cleanupQuestProbe removes the character through the protocol and the account
// through the data layer, in that order: an account that still owns characters is
// refused, on purpose.
//
// It dials its own session rather than reusing the test's, because the test's session
// is closed by then — and it retries once, because the server can still consider a
// just-closed character "logged in" for a moment and refuse the delete.
func cleanupQuestProbe(t *testing.T, ctx context.Context, data *wowdata.Client, charDB *sql.DB) {
	t.Helper()
	_ = charDB

	char, found, err := data.CharacterByName(ctx, questCharacter)
	if err != nil {
		t.Logf("cleanup: look up %s: %v", questCharacter, err)
		return
	}
	if found {
		for attempt := 1; attempt <= 2; attempt++ {
			if err := deleteProbeCharacter(ctx, char.GUID); err == nil {
				t.Logf("cleanup: character %s (%d) deleted", questCharacter, char.GUID)
				// The packet is not the fact: AC answers CMSG_CHAR_DELETE and commits
				// the row removal separately, and the account delete below is refused
				// while the row is still there.
				if err := waitProbeCharacterGone(ctx, data, 20*time.Second); err != nil {
					t.Logf("cleanup: %v", err)
				}
				break
			} else if attempt == 2 {
				t.Logf("cleanup: character %s is still there after two attempts: %v",
					questCharacter, err)
			} else {
				t.Logf("cleanup: deleting %s failed, retrying: %v", questCharacter, err)
				select {
				case <-time.After(10 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}
	}

	if err := data.DeleteAccount(ctx, questAccount); err != nil {
		t.Logf("cleanup: delete account %s: %v", questAccount, err)
		return
	}
	t.Logf("cleanup: account %s deleted", questAccount)
}

// waitProbeCharacterGone polls the characters table until the probe character is gone.
func waitProbeCharacterGone(ctx context.Context, data *wowdata.Client, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, found, err := data.CharacterByName(ctx, questCharacter); err != nil {
			return err
		} else if !found {
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return errors.New("character row outlived SMSG_CHAR_DELETE by " + timeout.String())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// deleteProbeCharacter dials, deletes one character from the character-select phase
// and closes again.
func deleteProbeCharacter(ctx context.Context, guid uint64) error {
	sess, err := wow.Dial(ctx, wow.ConnectOptions{
		AuthAddr: e2eharness.AuthAddr,
		Username: questAccount,
		Password: questPassword,
	})
	if err != nil {
		return err
	}
	defer sess.Close()

	return sess.DeleteCharacter(ctx, guid, 30*time.Second)
}
