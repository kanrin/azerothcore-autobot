package wow

import (
	"os"
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// TestConnectOptionsWithDefaults pins the retry and timeout values the rest of
// the login flow assumes. They are not arbitrary: three auth attempts and the
// timeouts come from upstream's e2e harness, so an accidental change here would
// make our login quieter and flakier than the one that is known to work.
func TestConnectOptionsWithDefaults(t *testing.T) {
	t.Run("zero value gets the documented defaults", func(t *testing.T) {
		got := ConnectOptions{}.withDefaults()
		if got.AuthAttempts != 3 {
			t.Errorf("AuthAttempts = %d, want 3", got.AuthAttempts)
		}
		if got.AuthTimeout != 30*time.Second {
			t.Errorf("AuthTimeout = %s, want 30s", got.AuthTimeout)
		}
		if got.WaitAuthedTimeout != 20*time.Second {
			t.Errorf("WaitAuthedTimeout = %s, want 20s", got.WaitAuthedTimeout)
		}
		if got.Logf == nil {
			t.Fatal("Logf is nil, want a no-op logger: a nil Logf would panic on the first log")
		}
		// The no-op must actually be callable.
		got.Logf("discarded %d", 1)
	})

	t.Run("explicit values survive", func(t *testing.T) {
		got := ConnectOptions{
			AuthAttempts:      1,
			AuthTimeout:       time.Second,
			WaitAuthedTimeout: 2 * time.Second,
		}.withDefaults()
		if got.AuthAttempts != 1 || got.AuthTimeout != time.Second || got.WaitAuthedTimeout != 2*time.Second {
			t.Fatalf("explicit options were overwritten: %+v", got)
		}
	})

	t.Run("negative values fall back to the defaults", func(t *testing.T) {
		// A negative timeout would otherwise reach time.NewTimer, which fires
		// immediately and turns the wait into a guaranteed failure.
		got := ConnectOptions{
			AuthAttempts:      -1,
			AuthTimeout:       -time.Second,
			WaitAuthedTimeout: -time.Second,
		}.withDefaults()
		if got.AuthAttempts != 3 || got.AuthTimeout != 30*time.Second || got.WaitAuthedTimeout != 20*time.Second {
			t.Fatalf("negative options did not fall back to defaults: %+v", got)
		}
	})
}

// TestApplyWorldLogLevel pins the E2E_WORLD_LOG contract from the project rules:
// unset and empty both mean AzerothGhost's Info default, a known level is
// applied, and an unrecognised value is ignored rather than fatal (upstream
// behaves the same). No server is involved: the client object is constructed but
// never connected, which is what makes this testable offline.
func TestApplyWorldLogLevel(t *testing.T) {
	newClient := func() *client.WorldClient {
		return client.NewWorldClient("Bot", []byte("session-key"), func(string, ...interface{}) {})
	}

	t.Run("unset falls back to info", func(t *testing.T) {
		// t.Setenv cannot unset, so manage this variable by hand.
		restoreEnv(t, "E2E_WORLD_LOG")

		w := newClient()
		w.SetLogLevel(client.LogWarn) // a previous run's level must not stick
		applyWorldLogLevel(w)
		if got := w.LogLevel(); got != client.LogInfo {
			t.Fatalf("unset E2E_WORLD_LOG gave level %v, want LogInfo", got)
		}
	})

	t.Run("accepted spellings are applied", func(t *testing.T) {
		// The spellings come from AzerothGhost's own ParseLogLevel, which also
		// accepts numbers and a couple of aliases; the bot must not narrow that.
		for _, tc := range []struct {
			env  string
			want client.LogLevel
		}{
			{"", client.LogInfo},
			{"trace", client.LogTrace},
			{"debug", client.LogDebug},
			{"info", client.LogInfo},
			{"warn", client.LogWarn},
			{"error", client.LogError},
			{"silent", client.LogSilent},
			{"4", client.LogDebug},
			{"off", client.LogSilent},
			{" WARNING ", client.LogWarn},
		} {
			t.Setenv("E2E_WORLD_LOG", tc.env)
			w := newClient()
			applyWorldLogLevel(w)
			if got := w.LogLevel(); got != tc.want {
				t.Errorf("E2E_WORLD_LOG=%q gave level %v, want %v", tc.env, got, tc.want)
			}
		}
	})

	t.Run("unrecognised value is ignored", func(t *testing.T) {
		t.Setenv("E2E_WORLD_LOG", "chatty")

		w := newClient()
		w.SetLogLevel(client.LogError)
		applyWorldLogLevel(w)
		if got := w.LogLevel(); got != client.LogError {
			t.Fatalf("an invalid E2E_WORLD_LOG changed the level to %v, want LogError", got)
		}
	})
}

// restoreEnv undoes any change the test makes to key, including removing it.
func restoreEnv(t *testing.T, key string) {
	t.Helper()

	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}
