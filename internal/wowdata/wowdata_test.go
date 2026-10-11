package wowdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The guard is the reason these tests can run offline: every write refuses a
// name outside the prefix before it touches a database handle, so a Client with
// nil handles still exercises the real code path.
func TestWritesRefuseNamesOutsideThePrefix(t *testing.T) {
	c := &Client{prefix: "autobot"}
	ctx := context.Background()

	if _, err := c.EnsureAccount(ctx, "realplayer", "secret"); !errors.Is(err, ErrNotBotName) {
		t.Errorf("EnsureAccount(realplayer) = %v, want ErrNotBotName", err)
	}
	if _, found, err := c.AccountID(ctx, "realplayer"); !errors.Is(err, ErrNotBotName) || found {
		t.Errorf("AccountID(realplayer) = (%v, %v), want (ErrNotBotName, false)", found, err)
	}
	if err := c.DeleteAccount(ctx, "realplayer"); !errors.Is(err, ErrNotBotName) {
		t.Errorf("DeleteAccount(realplayer) = %v, want ErrNotBotName", err)
	}
}

// TestBotPrefixIsCaseInsensitive matters because the client upper-cases the
// account name and the server stores it that way: a case-sensitive guard would
// refuse the very accounts this project provisions.
func TestBotPrefixIsCaseInsensitive(t *testing.T) {
	c := &Client{prefix: "autobot"}

	for _, name := range []string{"autobotprobe", "Autobotprobe", "AUTOBOTPROBE"} {
		if err := c.requireBotName("account", name); err != nil {
			t.Errorf("requireBotName(%q) = %v, want nil", name, err)
		}
	}
	if err := c.requireBotName("account", "autobtprobe"); err == nil {
		t.Error("requireBotName(autobtprobe) = nil, want an error for a near miss")
	}
}

func TestEnsureAccountRejectsEmptyPassword(t *testing.T) {
	c := &Client{prefix: "autobot"}

	_, err := c.EnsureAccount(context.Background(), "autobotprobe", "")
	if err == nil {
		t.Fatal("EnsureAccount with an empty password succeeded, want an error")
	}
	if errors.Is(err, ErrNotBotName) {
		t.Fatalf("error = %v, want a password complaint rather than a prefix one", err)
	}
}

func TestOpenRefusesIncompleteOptions(t *testing.T) {
	complete := Options{Prefix: "autobot", AuthDSN: "a", CharDSN: "b", WorldDSN: "c"}

	tests := []struct {
		name string
		opt  Options
		want error
	}{
		{"missing prefix", Options{AuthDSN: "a", CharDSN: "b", WorldDSN: "c"}, ErrNoPrefix},
		{"missing auth dsn", func() Options { o := complete; o.AuthDSN = ""; return o }(), ErrNoDSN},
		{"missing char dsn", func() Options { o := complete; o.CharDSN = ""; return o }(), ErrNoDSN},
		{"missing world dsn", func() Options { o := complete; o.WorldDSN = ""; return o }(), ErrNoDSN},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Open(tc.opt)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open = (%v, %v), want %v", c, err, tc.want)
			}
			if c != nil {
				t.Errorf("Open returned a client alongside the error: %+v", c)
			}
		})
	}
}

func TestPingAndCloseWithoutHandles(t *testing.T) {
	c := &Client{prefix: "autobot"}

	err := c.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("Ping = %v, want an error naming the auth database", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close on a client without handles = %v, want nil (closing must be idempotent)", err)
	}
}

// fakeScanner stands in for *sql.Row / *sql.Rows so the column projection can be
// checked without a database.
type fakeScanner struct {
	vals []any
	err  error
}

func (f fakeScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != len(f.vals) {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), len(f.vals))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *uint64:
			*p = f.vals[i].(uint64)
		case *string:
			*p = f.vals[i].(string)
		case *int:
			*p = f.vals[i].(int)
		case *uint8:
			*p = f.vals[i].(uint8)
		case *uint16:
			*p = f.vals[i].(uint16)
		default:
			return fmt.Errorf("fakeScanner: unsupported destination %T", d)
		}
	}
	return nil
}

func TestScanCharacter(t *testing.T) {
	ch, err := scanCharacter(fakeScanner{vals: []any{
		uint64(42), "Autobotprobe", 7, uint8(1), uint8(1), uint8(80), uint8(0), uint16(571),
	}})
	if err != nil {
		t.Fatalf("scanCharacter: %v", err)
	}

	want := Character{GUID: 42, Name: "Autobotprobe", AccountID: 7, Race: 1, Class: 1, Level: 80, Map: 571}
	if ch != want {
		t.Errorf("scanCharacter = %+v, want %+v", ch, want)
	}

	if _, err := scanCharacter(fakeScanner{err: sql.ErrNoRows}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("scanCharacter = %v, want the scanner's error to pass through", err)
	}
}
