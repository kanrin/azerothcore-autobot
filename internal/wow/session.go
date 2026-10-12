package wow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"azerothcore-autobot/internal/wow/packet"

	"github.com/azerothcore/AzerothGhost/client"
)

// Waiter errors.
var (
	// ErrTimeout means the awaited signal did not arrive before the deadline.
	// In the project's failure taxonomy this is an "R" (our reliability), never
	// a conclusion about the server: raise the timeout or fix the caller, but do
	// not relax an assertion because of it.
	ErrTimeout = errors.New("wow: timeout")

	// ErrClosed means the session was closed while the operation was running.
	ErrClosed = errors.New("wow: session closed")

	// ErrNotConnected means the session has no underlying world client. It can
	// only happen for a zero-value Session or one whose construction failed, and
	// exists so that those cases return an error instead of panicking on a nil
	// dereference deep inside the client.
	ErrNotConnected = errors.New("wow: not connected")

	// ErrLogoutRefused means the server refused CMSG_LOGOUT_REQUEST and named a
	// reason in the wrapped message. That is a precondition failure, not an
	// infrastructure one: the bot is in combat, falling, duelling, or otherwise
	// not allowed to leave yet. The server sends no completion packet in this
	// case, so without this error a refused logout would look like a timeout.
	ErrLogoutRefused = errors.New("wow: logout refused")
)

// PollInterval is how often condition-based waiters re-check their predicate.
// Internal polling inside a waiter is explicitly allowed (20-50ms); replacing a
// waiter with a bare time.Sleep is not.
const PollInterval = 25 * time.Millisecond

// Session is the single-writer command surface over one AzerothGhost
// client.WorldClient.
//
// Why this type exists instead of using the client directly:
//
//   - The client's send path is internally mutex-guarded, so concurrent writes
//     cannot corrupt the byte stream. What is not guarded is the *logical*
//     pairing of "send this, then wait for that reply". Session makes that
//     pairing the only supported way to talk to the server.
//   - Arm installs the reply listener before the triggering packet is written,
//     so a fast reply cannot be missed. That makes the required Arm -> Send ->
//     Wait pattern structural rather than a convention people forget.
//
// One Session maps to exactly one socket. Never open a second socket for the
// same account while the first is alive: the server keys the session on the auth
// session key, and the stale socket would then invalidate the new one.
type Session struct {
	client *client.WorldClient

	// opMu serialises whole command sequences (arm + send + wait). The packet
	// hook never takes it and the client's read goroutine never touches it, so
	// holding it across a wait cannot deadlock against the reader.
	opMu sync.Mutex

	mu     sync.Mutex
	closed bool

	// Character-select deliveries. AzerothGhost exposes OnCharList and
	// OnCharCreateResult as single-assignment callback fields, so Session owns
	// them and fans them into buffered channels. The callbacks are installed in
	// NewSession, which must therefore be called before the client's read loop
	// is started — assigning a callback field afterwards would race with the
	// read goroutine.
	charLists  chan []client.CharEnumEntry
	charCreate chan CharCreateResult

	// realmAddr is the world address from the authserver's realm list. It is
	// kept so that a failed login can report the address the server actually
	// handed out — the usual cause of "connection refused" on a LAN setup is a
	// realmlist that points at 127.0.0.1.
	realmAddr string
}

// NewSession wraps an already-constructed world client.
//
// Call it after Connect but *before* starting the read loop: NewSession installs
// the character-select callbacks, and writing a callback field while the read
// goroutine may be reading it is a data race.
func NewSession(w *client.WorldClient) *Session {
	s := &Session{
		client:     w,
		charLists:  make(chan []client.CharEnumEntry, 2),
		charCreate: make(chan CharCreateResult, 1),
	}
	w.OnCharList = s.deliverCharList
	w.OnCharCreateResult = s.deliverCharCreate
	return s
}

// Client returns the underlying AzerothGhost client.
//
// Prefer the typed methods on Session. This escape hatch exists for the upstream
// APIs that have no wrapper yet (movement, combat, group) and should shrink over
// time. Do not read unexported state through it, and do not use it to send
// packets from a second goroutine without going through the command lock.
func (s *Session) Client() *client.WorldClient { return s.client }

// RealmAddress is the world address the authserver returned in its realm list.
// It is empty until Dial has authenticated.
func (s *Session) RealmAddress() string { return s.realmAddr }

// Logout asks the server to end the session and waits for the session to be
// gone, reporting a refusal instead of a timeout when the server says no.
//
// DefaultLogoutTimeout bounds a whole graceful logout.
//
// AzerothCore only logs a character out instantly when it is resting, on a taxi,
// or privileged; otherwise WorldSession.h:547 is
// `curr_time >= _logout_time + 20`, so SMSG_LOGOUT_COMPLETE arrives about 20
// seconds after the request. Upstream's own e2e harness waits 30 seconds for
// exactly this reason. A shorter timeout here turns a normal logout into a
// spurious failure.
const DefaultLogoutTimeout = 30 * time.Second

// Logout sends CMSG_LOGOUT_REQUEST and waits for the session to end.
//
// Both replies are armed before the request leaves, because the server answers
// SMSG_LOGOUT_RESPONSE immediately while SMSG_LOGOUT_COMPLETE arrives only after
// the countdown:
//
//   - a non-zero reason in the response means the logout was refused (in combat,
//     falling, duelling). The server then sends nothing else, so a client that
//     waits only for completion reports a confusing timeout; this returns
//     ErrLogoutRefused with the reason right away.
//   - a zero reason means accepted; the completion packet follows.
//
// The command lock is held for the whole sequence, as in Do: the listener has to
// exist before the trigger packet is written, and a logout is the last thing a
// caller does with a session.
func (s *Session) Logout(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultLogoutTimeout
	}
	deadline := time.Now().Add(timeout)

	s.opMu.Lock()
	defer s.opMu.Unlock()

	exchange, err := s.Arm(SmsgLogoutResponse, SmsgLogoutComplete)
	if err != nil {
		return err
	}
	defer exchange.Stop()

	if err := s.send(CmsgLogoutRequest, nil); err != nil {
		return err
	}

	opcode, data, err := exchange.Next(ctx, timeout)
	if err != nil {
		return fmt.Errorf("wow: logout: %w", err)
	}
	if opcode == SmsgLogoutComplete {
		return nil // instant logout: the server skipped the response-then-countdown
	}

	response, err := packet.ParseLogoutResponse(data)
	if err != nil {
		return err
	}
	if response.Reason != packet.LogoutReasonNone {
		return fmt.Errorf("%w: %s", ErrLogoutRefused, response)
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return fmt.Errorf("wow: logout: %w", ErrTimeout)
	}
	if _, _, err := exchange.Next(ctx, remaining); err != nil {
		return fmt.Errorf("wow: logout: %w", err)
	}
	return nil
}

// Close tears the socket down. It is idempotent and must run on every failure
// path: a socket left open keeps authenticating with the previous session key
// and causes AUTH_FAILED cascades for the next login attempt.
func (s *Session) Close() {
	s.mu.Lock()
	alreadyClosed := s.closed
	s.closed = true
	s.mu.Unlock()

	if alreadyClosed || s.client == nil {
		return
	}
	s.client.Close()
}

// checkClosed reports ErrClosed once Close has run.
func (s *Session) checkClosed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

// send writes one packet without taking the command lock. Callers inside a
// command sequence use this; everything else uses Send.
func (s *Session) send(opcode uint16, payload []byte) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	if s.client == nil {
		return fmt.Errorf("wow: send %s: %w", OpcodeName(opcode), ErrNotConnected)
	}
	if err := s.client.SendPacketRaw(opcode, payload); err != nil {
		return fmt.Errorf("wow: send %s: %w", OpcodeName(opcode), err)
	}
	return nil
}

// Send writes a packet with no reply to wait for, for example a periodic
// keep-alive or a fire-and-forget notification.
//
// Anything that expects an answer must use Do instead, so that the listener is
// armed before the packet leaves.
func (s *Session) Send(opcode uint16, payload []byte) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.send(opcode, payload)
}

// Do runs one command atomically and returns the first matching reply.
//
// It arms a listener for replies, sends the packet, and waits for the first of
// those opcodes to arrive. The whole sequence holds the command lock, so two
// concurrent callers cannot steal each other's replies.
//
// Because the lock is held across the wait, a caller must not invoke Do from
// inside another Do (that would deadlock) and should not hold the lock for a
// long timeout when other work needs the socket. The agent runs one pending
// action at a time by design, which makes this the right trade-off.
func (s *Session) Do(ctx context.Context, opcode uint16, payload []byte, replies []uint16, timeout time.Duration) (uint16, []byte, error) {
	if len(replies) == 0 {
		return 0, nil, fmt.Errorf("wow: Do(%s) needs at least one reply opcode", OpcodeName(opcode))
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	exchange, err := s.Arm(replies...)
	if err != nil {
		return 0, nil, err
	}
	defer exchange.Stop()

	if err := s.send(opcode, payload); err != nil {
		return 0, nil, err
	}

	gotOpcode, data, err := exchange.Next(ctx, timeout)
	if err != nil {
		return 0, nil, fmt.Errorf("wow: %s: %w", OpcodeName(opcode), err)
	}
	return gotOpcode, data, nil
}

// doWithLock runs fn while holding the command lock.
//
// It exists for AzerothGhost methods that send a packet but have their own
// signature and side effects (RequestCharList, CreateCharacter,
// LoginCharacter). Going through here keeps them serialised against Do and Send
// instead of writing to the socket behind the command lock's back.
func (s *Session) doWithLock(fn func() error) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	if err := s.checkClosed(); err != nil {
		return err
	}
	if s.client == nil {
		return ErrNotConnected
	}
	return fn()
}
