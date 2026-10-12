//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"azerothcore-autobot/internal/wowdata"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// cliAccount is the account this case provisions. It is deliberately different
// from the smoke account so the two never race over the same row, and it is
// deleted before the test returns.
const cliAccount = "autobote2eacct"

// TestAccountLifecycle covers the data layer behind `autobot account create`,
// `list` and `delete` on a live auth database:
//
//   - create is idempotent, a second call refreshes the password instead of
//     inserting a duplicate;
//   - the listing is filtered by the bot prefix, so it can never show a real
//     player's account;
//   - delete removes the row and is idempotent too, which is what makes it a
//     usable cleanup path.
//
// The guard itself (refusing a name outside the prefix) is a unit test in
// internal/wowdata, because it must hold before any database is involved.
func TestAccountLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	data := openData(t, smokePrefix)
	t.Cleanup(func() {
		// Cleanup runs even when an assertion failed: a leftover account would
		// otherwise be picked up by the next run and hide a real failure.
		_ = data.DeleteAccount(context.Background(), cliAccount)
	})

	created, err := data.EnsureAccount(ctx, cliAccount, "e2epassword")
	if err != nil {
		t.Fatalf("create account %s: %v", cliAccount, err)
	}
	if !created.Created || created.ID <= 0 {
		t.Fatalf("first create = %+v, want Created=true and a positive id", created)
	}
	t.Logf("created account %s (id %d)", created.Username, created.ID)

	again, err := data.EnsureAccount(ctx, cliAccount, "e2epassword")
	if err != nil {
		t.Fatalf("second create of %s: %v", cliAccount, err)
	}
	if again.Created || again.ID != created.ID {
		t.Errorf("second create = %+v, want Created=false and the same id %d", again, created.ID)
	}

	rows, err := data.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	found := false
	for _, row := range rows {
		if !strings.HasPrefix(strings.ToLower(row.Username), smokePrefix) {
			t.Errorf("listing returned %q, which is outside the prefix %q", row.Username, smokePrefix)
		}
		if strings.EqualFold(row.Username, cliAccount) {
			found = true
			if row.ID != created.ID {
				t.Errorf("listed id for %s = %d, want %d", cliAccount, row.ID, created.ID)
			}
		}
	}
	if !found {
		t.Errorf("the listing does not contain %s: %+v", cliAccount, rows)
	}

	if err := data.DeleteAccount(ctx, cliAccount); err != nil {
		t.Fatalf("delete account %s: %v", cliAccount, err)
	}
	if err := data.DeleteAccount(ctx, cliAccount); err != nil {
		t.Errorf("deleting %s twice = %v, want nil (the cleanup path must be idempotent)", cliAccount, err)
	}

	rows, err = data.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts after delete: %v", err)
	}
	for _, row := range rows {
		if strings.EqualFold(row.Username, cliAccount) {
			t.Errorf("account %s is still listed after being deleted", cliAccount)
		}
	}
	t.Logf("account %d is gone; %d account(s) left under prefix %q", created.ID, len(rows), smokePrefix)
}

// openData opens the data layer with the given prefix guard. The account commands
// and the live tests share it so that one place decides which DSNs and which
// prefix are in play.
func openData(t *testing.T, prefix string) *wowdata.Client {
	t.Helper()

	data, err := wowdata.Open(wowdata.Options{
		AuthDSN:  e2eharness.AuthDSN,
		CharDSN:  e2eharness.CharDSN,
		WorldDSN: e2eharness.WorldDSN,
		Prefix:   prefix,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("precondition: open data layer: %v", err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}
