package wow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// ConnectOptions describes how to reach one realm.
type ConnectOptions struct {
	// AuthAddr is the authserver address, host:port.
	AuthAddr string

	// Username and Password are the account credentials. AzerothGhost upper-cases
	// both, matching what the server stores.
	Username string
	Password string

	// RealmIndex picks an entry from the realm list the authserver returns.
	RealmIndex int

	// AuthAttempts is how many times the SRP6 handshake is retried. The
	// authserver is single-threaded, so concurrent logons intermittently time
	// out while reading the logon proof; upstream retries three times and so
	// does this.
	AuthAttempts int

	// AuthTimeout bounds one whole auth attempt (dial + SRP6 + realm list).
	AuthTimeout time.Duration

	// WaitAuthedTimeout bounds waiting for SMSG_AUTH_RESPONSE on the world
	// socket.
	WaitAuthedTimeout time.Duration

	// Logf receives AzerothGhost's own diagnostics. Nil discards them.
	Logf func(format string, args ...interface{})
}

// withDefaults fills in the values this project uses when a caller leaves a
// field zero.
func (o ConnectOptions) withDefaults() ConnectOptions {
	if o.AuthAttempts <= 0 {
		o.AuthAttempts = 3
	}
	if o.AuthTimeout <= 0 {
		o.AuthTimeout = 30 * time.Second
	}
	if o.WaitAuthedTimeout <= 0 {
		o.WaitAuthedTimeout = 20 * time.Second
	}
	if o.Logf == nil {
		o.Logf = func(string, ...interface{}) {}
	}
	return o
}

// AuthResult is what the authserver told us: the realm list, the SRP6 session
// key the world socket must be opened with, and the selected realm.
type AuthResult struct {
	Realms     []client.RealmInfo
	SessionKey []byte
	Realm      client.RealmInfo
}

// Authenticate performs the SRP6 logon and returns the realm list plus the
// session key.
//
// The whole handshake is retried because a single failure here is usually the
// authserver's single network thread being busy rather than bad credentials;
// upstream's e2e suite retries the same way. The backoff between attempts is the
// only place in this package where a bare wait is appropriate — it is a retry
// delay, not a substitute for waiting on a signal.
func Authenticate(ctx context.Context, opt ConnectOptions) (AuthResult, error) {
	opt = opt.withDefaults()

	var lastErr error
	for attempt := 1; attempt <= opt.AuthAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return AuthResult{}, err
		}

		auth := client.NewAuthClient(opt.Username, opt.Password)
		realms, err := authenticateOnce(ctx, auth, opt)

		if err == nil && len(realms) > 0 {
			if opt.RealmIndex < 0 || opt.RealmIndex >= len(realms) {
				return AuthResult{}, fmt.Errorf("wow: realm index %d out of range, authserver returned %d realm(s)",
					opt.RealmIndex, len(realms))
			}
			return AuthResult{
				Realms:     realms,
				SessionKey: append([]byte(nil), auth.SessionKey()...),
				Realm:      realms[opt.RealmIndex],
			}, nil
		}
		if err == nil {
			err = fmt.Errorf("authserver returned an empty realm list")
		}
		lastErr = err
		opt.Logf("auth attempt %d/%d failed: %v", attempt, opt.AuthAttempts, err)

		if attempt == opt.AuthAttempts {
			break
		}
		if err := sleepCtx(ctx, time.Duration(attempt)*300*time.Millisecond); err != nil {
			return AuthResult{}, err
		}
	}
	return AuthResult{}, fmt.Errorf("wow: auth failed after %d attempt(s): %w", opt.AuthAttempts, lastErr)
}

// authenticateOnce runs one SRP6 attempt with a deadline.
//
// AuthClient.Authenticate is blocking and not context-aware; it bounds itself
// with a 30s connection deadline, so a cancelled context lingers at most that
// long before the goroutine exits.
func authenticateOnce(ctx context.Context, auth *client.AuthClient, opt ConnectOptions) ([]client.RealmInfo, error) {
	type outcome struct {
		realms []client.RealmInfo
		err    error
	}

	done := make(chan outcome, 1)
	go func() {
		realms, err := auth.Authenticate(opt.AuthAddr)
		done <- outcome{realms: realms, err: err}
	}()

	timer := time.NewTimer(opt.AuthTimeout)
	defer timer.Stop()

	select {
	case out := <-done:
		return out.realms, out.err
	case <-timer.C:
		return nil, fmt.Errorf("auth to %s timed out after %s", opt.AuthAddr, opt.AuthTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Dial authenticates, opens the world socket and waits until the realm accepts
// the session (PhaseAuthed).
//
// The returned session has not logged a character in yet; it still needs the
// character-list handshake, which Session.EnterWorld performs.
//
// Every failure path closes the socket. That matters more than it looks: the
// server keys a session on the SRP6 session key, so a socket left open keeps
// authenticating with the key of a *previous* attempt and the next logon fails
// with AUTH_FAILED even though the credentials are correct.
func Dial(ctx context.Context, opt ConnectOptions) (*Session, error) {
	opt = opt.withDefaults()

	auth, err := Authenticate(ctx, opt)
	if err != nil {
		return nil, err
	}
	opt.Logf("authenticated realm=%s addr=%s", auth.Realm.Name, auth.Realm.Address)

	w := client.NewWorldClient(strings.ToUpper(opt.Username), auth.SessionKey, opt.Logf)
	applyWorldLogLevel(w)

	if err := w.Connect(auth.Realm.Address); err != nil {
		w.Close()
		return nil, fmt.Errorf("wow: connect world %s: %w", auth.Realm.Address, err)
	}

	// NewSession installs the character-select callbacks, so it must run before
	// the read loop starts.
	sess := NewSession(w)
	sess.realmAddr = auth.Realm.Address

	// AzerothGhost owns the read loop; we only ever write through Session.
	go func() { _ = w.Run() }()

	// WaitForSessionPhase is event-driven and unblocks when the client stops, so
	// a rejected logon (the client logs "Auth response failed" and closes)
	// reports "client stopped while waiting" instead of hanging until timeout.
	if err := waitUpstream(ctx, "SMSG_AUTH_RESPONSE (PhaseAuthed)", opt.WaitAuthedTimeout,
		func(timeout time.Duration) error {
			return w.WaitForSessionPhase(client.PhaseAuthed, timeout)
		}); err != nil {
		sess.Close()
		return nil, err
	}

	return sess, nil
}

// applyWorldLogLevel honours E2E_WORLD_LOG, the same variable AzerothGhost's own
// e2e suite reads, so one setting covers our runs and upstream tooling alike.
// Unset means the client's Info default; an unrecognised value is ignored rather
// than treated as an error, matching upstream.
func applyWorldLogLevel(w *client.WorldClient) {
	raw, ok := os.LookupEnv("E2E_WORLD_LOG")
	if !ok {
		w.SetLogLevel(client.LogInfo)
		return
	}
	if level, ok := client.ParseLogLevel(raw); ok {
		w.SetLogLevel(level)
	}
}
