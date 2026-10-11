package wow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Exchange collects the replies to one command. It is created by Arm, which
// must be called *before* the packet that triggers the reply is sent.
type Exchange struct {
	wanted map[uint16]struct{}
	events chan packetEvent
	stop   func()
	once   sync.Once
}

// packetEvent is one delivered reply.
//
// data aliases the slice the client allocated for this packet; AzerothGhost's
// readPacket allocates a fresh slice per packet (client/world.go:1477), so
// handing it to a channel cannot race with the next read and no copy is needed.
type packetEvent struct {
	opcode uint16
	data   []byte
}

// Arm starts collecting packets whose opcode is in opcodes.
//
// This is the "Arm" step of Arm -> Send -> Wait. Calling it before the send is
// what makes the pattern reliable: the hook is already installed when the reply
// is dispatched on the client's read goroutine.
//
// The caller owns the returned Exchange and must Stop it; Do does that with
// defer.
//
// An error is returned only for a session with no client, i.e. one that was
// never connected: there is no hook registry to install into, and returning a
// live-looking Exchange would turn a programming error into a silent timeout.
func (s *Session) Arm(opcodes ...uint16) (*Exchange, error) {
	if s.client == nil {
		return nil, ErrNotConnected
	}

	ex := &Exchange{
		wanted: make(map[uint16]struct{}, len(opcodes)),
		// Buffered so the hook never blocks the client's read goroutine. A
		// handful of queued replies is plenty: the waiter only ever consumes
		// one, and anything beyond the buffer size means the caller is not
		// draining exchanges, which we surface as a dropped packet rather than
		// a stalled read loop.
		events: make(chan packetEvent, 16),
	}
	for _, op := range opcodes {
		ex.wanted[op] = struct{}{}
	}

	ex.stop = s.client.AddPacketHook(func(opcode uint16, data []byte) {
		if _, ok := ex.wanted[opcode]; !ok {
			return
		}
		select {
		case ex.events <- packetEvent{opcode: opcode, data: data}:
		default:
			// Deliberately dropped. Blocking here would stall packet dispatch
			// for every other hook and for the client's own handlers.
		}
	})

	return ex, nil
}

// Next blocks until one of the armed packets arrives, ctx is done, or timeout
// elapses. It returns the opcode that arrived so the caller can branch on it.
func (e *Exchange) Next(ctx context.Context, timeout time.Duration) (uint16, []byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case ev := <-e.events:
		return ev.opcode, ev.data, nil
	case <-timer.C:
		return 0, nil, fmt.Errorf("waiting for %s: %w", e.describe(), ErrTimeout)
	case <-ctx.Done():
		return 0, nil, fmt.Errorf("waiting for %s: %w", e.describe(), ctx.Err())
	}
}

// NextOpcode is Next for callers that only care which reply arrived.
func (e *Exchange) NextOpcode(ctx context.Context, timeout time.Duration) (uint16, error) {
	opcode, _, err := e.Next(ctx, timeout)
	return opcode, err
}

// Stop removes the hook. It is safe to call more than once, which lets Do use
// defer while a caller may also stop the exchange early.
func (e *Exchange) Stop() {
	if e.stop == nil {
		return
	}
	e.once.Do(e.stop)
}

// describe names the armed opcodes for error messages, so a timeout says which
// reply was missing instead of just "timeout".
func (e *Exchange) describe() string {
	names := make([]string, 0, len(e.wanted))
	for op := range e.wanted {
		names = append(names, OpcodeName(op))
	}
	if len(names) == 0 {
		return "no opcodes"
	}
	sort.Strings(names)
	return strings.Join(names, " or ")
}

// AwaitReply arms a listener for replies and then waits for one of them.
//
// Use this only for signals the server originates on its own schedule (a group
// invite, an LFG proposal, a status broadcast). Any reply triggered by a packet
// we send must go through Do instead, so the listener is provably armed before
// the trigger leaves.
//
// The caveat is real and worth repeating: because the hook is installed inside
// this call, a reply that arrived earlier is already gone. For a flow with
// several unrelated signals over a longer period, Arm one exchange up front and
// consume it in a loop.
func (s *Session) AwaitReply(ctx context.Context, timeout time.Duration, replies ...uint16) (uint16, []byte, error) {
	if len(replies) == 0 {
		return 0, nil, errors.New("wow: AwaitReply needs at least one opcode")
	}

	exchange, err := s.Arm(replies...)
	if err != nil {
		return 0, nil, err
	}
	defer exchange.Stop()

	return exchange.Next(ctx, timeout)
}
