package wow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
