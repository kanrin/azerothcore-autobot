//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"azerothcore-autobot/internal/bot"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// TestRunResidentLoop is milestone 1's acceptance run: the bot stays in the world
// for a while, ticking, and then stops on demand with a logout that the server
// agrees with.
//
// It drives internal/bot directly, which is the same code `autobot run` uses
// (the CLI adds flag parsing and signal handling on top). Travel is deliberately
// not enabled: walking needs the mmap data, so it has its own case once a
// pathfinding source is available — what is tested here is the loop, the
// keep-alive and the shutdown order.
func TestRunResidentLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	ensureSmokeAccount(t, ctx)
	charDB := openDB(t, "characters", e2eharness.CharDSN)
	data := openData(t, smokePrefix)

	runner, err := bot.New(bot.Spec{
		Name:       smokeCharName,
		AuthAddr:   e2eharness.AuthAddr,
		RealmIndex: 0,
		Account:    smokeAccount,
		Password:   smokePassword,
		Character:  smokeCharName,
		Race:       1,
		Class:      1,
	}, bot.Options{
		Tick:   100 * time.Millisecond,
		Logf:   t.Logf,
		Logger: testLogger(t),
	})
	if err != nil {
		t.Fatalf("bot.New: %v", err)
	}

	if err := runner.Login(ctx, data); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !runner.Session().InWorld() {
		t.Fatal("the session is not in the world after Login")
	}
	if runner.Character().GUID == 0 {
		t.Fatal("the runner has no character guid after Login")
	}

	// Run the loop, then stop it the way the CLI does: cancel, wait for the loop
	// to return, then close. The loop must be ticking before the cancel, otherwise
	// the test would pass on a bot that never started playing.
	playCtx, playCancel := context.WithCancel(ctx)
	playDone := make(chan error, 1)
	go func() { playDone <- runner.Play(playCtx) }()

	waitTicks(t, runner, 5, 20*time.Second)
	bb := runner.Blackboard()
	if self, ok := bot.SelfOf(bb); !ok {
		t.Error("the runner never published its self state")
	} else if self.Health == 0 || self.MaxHealth == 0 {
		t.Errorf("self state after %d ticks = %+v, want real health", bb.Tick(), self)
	}

	playCancel()
	select {
	case err := <-playDone:
		if err != nil {
			t.Fatalf("Play returned %v, want nil after a cancelled context", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Play did not return after the context was cancelled")
	}

	// The shutdown is a real logout: the server only clears `online` once the
	// session is gone, so the database is the evidence, not the log line.
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelClose()
	if err := runner.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitCharacterOffline(t, charDB, smokeCharName, 30*time.Second)
	t.Logf("resident loop: %d ticks, logged out, character is offline", bb.Tick())
}

// waitTicks waits until the agent has completed n ticks. The tick counter is the
// real signal that the loop is running; sleeping and hoping would pass even if
// the loop never started.
func waitTicks(t *testing.T, runner *bot.Runner, n int64, timeout time.Duration) {
	t.Helper()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		if ticks := runner.Blackboard().Tick(); ticks >= n {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("the agent ran %d ticks in %s, want at least %d",
				runner.Blackboard().Tick(), timeout, n)
		}
	}
}

// waitCharacterOffline polls until the server has marked the character offline.
func waitCharacterOffline(t *testing.T, charDB *sql.DB, name string, timeout time.Duration) {
	t.Helper()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		var online uint8
		err := charDB.QueryRow(`SELECT online FROM characters WHERE name = ?`, name).Scan(&online)
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("character %s is gone from the database", name)
		}
		if err != nil {
			t.Fatalf("read online state of %s: %v", name, err)
		}
		if online == 0 {
			return
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("character %s is still online %s after the logout", name, timeout)
		}
	}
}

// testLogger routes the runner's structured lines into the test log, so a failure
// shows what the bot was doing without turning the run into noise on stdout.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(&testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// testWriter sends one line per write to t.Logf.
type testWriter struct{ t *testing.T }

func (w *testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", string(p))
	return len(p), nil
}

var _ io.Writer = (*testWriter)(nil)
