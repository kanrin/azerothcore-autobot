package wow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// newOfflineSession builds a Session with live delivery channels but no socket.
//
// The character-select logic (delivery fan-in, name matching, timeout handling)
// is deliberately kept out of the network path so it can be tested without a
// server: those are the parts where a bug is invisible at runtime, because a
// dropped or stale reply only shows up as an unexplained "no such character".
func newOfflineSession() *Session {
	return &Session{
		charLists:  make(chan []client.CharEnumEntry, 2),
		charCreate: make(chan CharCreateResult, 1),
	}
}

// TestSessionDoneSignalsRunEnd covers the disconnect signal the bot layer waits
// on: the read loop ending has to be observable, has to keep the first error, and
// has to be safe to report twice (Close and the loop both report it).
func TestSessionDoneSignalsRunEnd(t *testing.T) {
	s := &Session{}

	select {
	case <-s.Done():
		t.Fatal("Done was already closed before the read loop ended")
	default:
	}

	boom := errors.New("connection reset")
	s.markRunDone(boom)
	s.markRunDone(errors.New("a second, later error")) // idempotent: the first one wins

	select {
	case <-s.Done():
	default:
		t.Fatal("Done is not closed after the read loop ended")
	}
	if !errors.Is(s.RunErr(), boom) {
		t.Errorf("RunErr = %v, want %v", s.RunErr(), boom)
	}
}

// TestSessionDoneIsNotClosedBeforeTheLoopEnds keeps "has not ended yet" and
// "ended" distinguishable, which is the whole point of the channel.
func TestSessionDoneIsNotClosedBeforeTheLoopEnds(t *testing.T) {
	s := &Session{}
	if s.Done() == nil {
		t.Fatal("Done returned a nil channel")
	}
	select {
	case <-s.Done():
		t.Fatal("Done is closed before anything ended the read loop")
	default:
	}
	// Close on a session without a client still has to unblock a waiter.
	s.markRunDone(nil)
	select {
	case <-s.Done():
	default:
		t.Fatal("Done is still open after the loop was reported as ended")
	}
}

func TestSessionSendWithoutClientFails(t *testing.T) {
	s := newOfflineSession()

	err := s.Send(CmsgGossipHello, []byte{1, 2, 3})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send = %v, want ErrNotConnected", err)
	}
	if !strings.Contains(err.Error(), OpcodeName(CmsgGossipHello)) {
		t.Errorf("Send error %q should name the opcode %s", err, OpcodeName(CmsgGossipHello))
	}
}

func TestSessionSendAfterCloseFails(t *testing.T) {
	s := newOfflineSession()
	s.Close()

	if err := s.Send(CmsgGossipHello, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
}

// Close runs on every failure path, including ones where Dial never finished
// connecting. It must therefore be safe on a client-less session, and safe to
// call twice.
func TestSessionCloseWithoutClientIsSafe(t *testing.T) {
	s := newOfflineSession()
	s.Close()
	s.Close()

	if err := s.checkClosed(); !errors.Is(err, ErrClosed) {
		t.Fatalf("checkClosed after Close = %v, want ErrClosed", err)
	}
}

// A command with no reply opcode is a caller bug: it would either arm nothing or
// wait forever. It must be rejected before touching the socket, so it fails the
// same way whether or not a server is reachable.
func TestSessionDoRejectsEmptyReplyList(t *testing.T) {
	s := newOfflineSession()

	_, _, err := s.Do(context.Background(), CmsgLfgJoin, nil, nil, time.Second)
	if err == nil {
		t.Fatal("Do with no reply opcodes returned no error")
	}
	if errors.Is(err, ErrNotConnected) {
		t.Fatalf("Do reported %v before validating its arguments", ErrNotConnected)
	}
	if !strings.Contains(err.Error(), "reply opcode") {
		t.Errorf("error %q should explain that a reply opcode is required", err)
	}
}

func TestSessionDoWithoutClientFails(t *testing.T) {
	s := newOfflineSession()

	_, _, err := s.Do(context.Background(), CmsgLfgJoin, []byte{1}, []uint16{SmsgLfgJoinResult}, time.Second)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Do = %v, want ErrNotConnected", err)
	}
}

func TestSessionAwaitReplyValidatesArguments(t *testing.T) {
	s := newOfflineSession()

	if _, _, err := s.AwaitReply(context.Background(), time.Second); err == nil {
		t.Fatal("AwaitReply with no opcodes returned no error")
	}
	if _, _, err := s.AwaitReply(context.Background(), time.Second, SmsgLfgProposalUpdate); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("AwaitReply = %v, want ErrNotConnected", err)
	}
}

// The create reply channel has room for one value, so a second delivery must
// displace the first. Otherwise a late answer to a name the server already
// rejected would be read as the outcome of the next attempt.
func TestSessionDeliverCharCreateKeepsNewest(t *testing.T) {
	s := newOfflineSession()

	s.deliverCharCreate([]byte{byte(CharCreateNameInUse)})
	s.deliverCharCreate([]byte{byte(CharCreateSuccess)})

	code, err := s.waitCharCreate(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("waitCharCreate: %v", err)
	}
	if code != CharCreateSuccess {
		t.Fatalf("waitCharCreate = %s, want %s", code, CharCreateSuccess)
	}

	select {
	case extra := <-s.charCreate:
		t.Fatalf("channel still holds %s after the newest reply was consumed", extra)
	default:
	}
}

// An empty SMSG_CHAR_CREATE payload cannot be interpreted. Queueing it would make
// waitCharCreate report a code that never came off the wire.
func TestSessionDeliverCharCreateIgnoresEmptyPayload(t *testing.T) {
	s := newOfflineSession()

	s.deliverCharCreate(nil)

	select {
	case code := <-s.charCreate:
		t.Fatalf("empty payload delivered code %s", code)
	default:
	}
}

func TestSessionDeliverCharListKeepsNewest(t *testing.T) {
	s := newOfflineSession()

	entry := func(name string) client.CharEnumEntry {
		return client.CharEnumEntry{Name: name}
	}
	s.deliverCharList([]client.CharEnumEntry{entry("stale")})
	s.deliverCharList([]client.CharEnumEntry{entry("older")})
	s.deliverCharList([]client.CharEnumEntry{entry("newest")})

	// Two slots, three deliveries: the oldest must have been displaced and the
	// newest must be present.
	var got []string
drain:
	for {
		select {
		case list := <-s.charLists:
			for _, c := range list {
				got = append(got, c.Name)
			}
		default:
			break drain
		}
	}

	if len(got) != 2 || got[0] != "older" || got[1] != "newest" {
		t.Fatalf("queued lists = %v, want [older newest]", got)
	}
}

// The callback hands out the client's slice; mutating it afterwards must not
// change what the queued list contains, because the client reuses its decode
// buffers between packets.
func TestSessionDeliverCharListCopiesTheSlice(t *testing.T) {
	s := newOfflineSession()

	entries := []client.CharEnumEntry{{Name: "Thrall"}}
	s.deliverCharList(entries)
	entries[0].Name = "Mutated"

	list := <-s.charLists
	if list[0].Name != "Thrall" {
		t.Fatalf("queued entry = %q, want %q; the delivery aliased the caller's slice", list[0].Name, "Thrall")
	}
}

func TestSessionWaitCharCreate(t *testing.T) {
	t.Run("returns the delivered code", func(t *testing.T) {
		s := newOfflineSession()
		s.deliverCharCreate([]byte{byte(CharCreateNameInUse)})

		code, err := s.waitCharCreate(context.Background(), time.Second)
		if err != nil {
			t.Fatalf("waitCharCreate: %v", err)
		}
		if code != CharCreateNameInUse {
			t.Fatalf("code = %s, want %s", code, CharCreateNameInUse)
		}
	})

	t.Run("times out", func(t *testing.T) {
		s := newOfflineSession()

		_, err := s.waitCharCreate(context.Background(), 40*time.Millisecond)
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("waitCharCreate = %v, want ErrTimeout", err)
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		s := newOfflineSession()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := s.waitCharCreate(ctx, time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitCharCreate = %v, want context.Canceled", err)
		}
	})
}

// requestCharList goes through the client's RequestCharList, so a client-less
// session must report that rather than panic inside AzerothGhost.
func TestSessionRequestCharListWithoutClientFails(t *testing.T) {
	s := newOfflineSession()

	_, err := s.requestCharList(context.Background(), time.Second)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("requestCharList = %v, want ErrNotConnected", err)
	}
}

func TestSessionEnterWorldWithoutClientFails(t *testing.T) {
	s := newOfflineSession()
	// Leftovers from an earlier handshake must be dropped by EnterWorld so they
	// cannot be mistaken for the answer to its own request.
	s.deliverCharList([]client.CharEnumEntry{{Name: "stale"}})
	s.deliverCharCreate([]byte{byte(CharCreateSuccess)})

	_, err := s.EnterWorld(context.Background(), EnterWorldOptions{Name: "Thrall", Race: 1, Class: 1})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("EnterWorld = %v, want ErrNotConnected", err)
	}

	select {
	case list := <-s.charLists:
		t.Fatalf("EnterWorld left %d queued character list(s) behind", len(list))
	default:
	}
	select {
	case code := <-s.charCreate:
		t.Fatalf("EnterWorld left queued create result %s behind", code)
	default:
	}
}

func TestSessionWaitFor(t *testing.T) {
	s := newOfflineSession()

	t.Run("returns immediately when already true", func(t *testing.T) {
		if err := s.WaitFor(context.Background(), time.Second, "already true", func() bool { return true }); err != nil {
			t.Fatalf("WaitFor = %v, want nil", err)
		}
	})

	t.Run("times out and names the condition", func(t *testing.T) {
		err := s.WaitFor(context.Background(), 40*time.Millisecond, "unit in sight", func() bool { return false })
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("WaitFor = %v, want ErrTimeout", err)
		}
		if !strings.Contains(err.Error(), "unit in sight") {
			t.Errorf("error %q should name the awaited condition", err)
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := s.WaitFor(ctx, time.Second, "unit in sight", func() bool { return false })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitFor = %v, want context.Canceled", err)
		}
	})

	t.Run("polls until the condition flips", func(t *testing.T) {
		// The predicate is false on the first check, so returning nil at all
		// proves the loop re-evaluated it instead of sleeping once. The timeout
		// is generous on purpose: this asserts behaviour, not timing.
		tries := 0
		cond := func() bool {
			tries++
			return tries > 1
		}
		if err := s.WaitFor(context.Background(), 4*PollInterval, "second try", cond); err != nil {
			t.Fatalf("WaitFor = %v, want nil after the second poll", err)
		}
		if tries < 2 {
			t.Fatalf("condition ran %d time(s), want at least 2", tries)
		}
	})
}
