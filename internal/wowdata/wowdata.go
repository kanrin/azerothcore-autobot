// Package wowdata is the data layer over the three AzerothCore databases.
//
// It owns the only writes this project is allowed to make — provisioning a bot
// account and taking it away again — and it is the only package that reads the
// databases for world knowledge and for checking what the server actually did.
//
// Two rules are enforced in code rather than by convention:
//
//   - Every name a write touches must start with the bot prefix (Options.Prefix).
//     A name that does not is refused, so a mistyped profile can never reach a
//     real player's account or character.
//   - Every write is idempotent and has a counterpart that takes it away again
//     (DeleteAccount; deleting a character is a protocol operation and therefore
//     lives in internal/wow).
package wowdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"

	// The driver has to be registered by whoever opens a handle; e2eharness only
	// deals in database/sql.
	_ "github.com/go-sql-driver/mysql"
)

// Data-layer errors.
var (
	// ErrNoPrefix means Options.Prefix was empty. Without a prefix the safety
	// guard below cannot work, so opening is refused outright.
	ErrNoPrefix = errors.New("wowdata: bot prefix is required")

	// ErrNotBotName means a write was attempted on a name outside the prefix.
	ErrNotBotName = errors.New("wowdata: name outside the bot prefix")

	// ErrAccountNotEmpty means an account still owns characters. Deleting it
	// anyway would leave character rows behind that reappear the moment an
	// account with the same name is provisioned again.
	ErrAccountNotEmpty = errors.New("wowdata: account still owns characters")

	// ErrNoDSN means one of the required DSNs was empty.
	ErrNoDSN = errors.New("wowdata: database DSN is required")
)

// dbTimeout bounds one connection check while opening.
const dbTimeout = 5 * time.Second

// Options describes how to reach the databases and which names are ours.
type Options struct {
	// AuthDSN and CharDSN are required; WorldDSN is opened too (the world
	// database is read-only world knowledge for the feature layers).
	AuthDSN  string
	CharDSN  string
	WorldDSN string

	// Prefix marks every account and character this project owns. Required.
	Prefix string

	// Logf receives progress lines. Nil discards them.
	Logf func(format string, args ...interface{})
}

// Client bundles the three database handles plus the prefix guard.
type Client struct {
	auth  *sql.DB
	char  *sql.DB
	world *sql.DB

	prefix string
	logf   func(format string, args ...interface{})
}

// Open connects to all three databases and verifies each one answers.
//
// Failing here is an environment error: the caller reports it and stops rather
// than retrying, because a wrong DSN never fixes itself. DSN values are never
// echoed — they contain the database password — only the name of the variable
// the operator should look at.
func Open(opt Options) (*Client, error) {
	if opt.Prefix == "" {
		return nil, fmt.Errorf("%w: set account.prefix in the profile", ErrNoPrefix)
	}
	if opt.AuthDSN == "" || opt.CharDSN == "" || opt.WorldDSN == "" {
		return nil, fmt.Errorf("%w: auth=%t char=%t world=%t (see E2E_AUTH_DSN, E2E_CHAR_DSN, E2E_WORLD_DSN)",
			ErrNoDSN, opt.AuthDSN != "", opt.CharDSN != "", opt.WorldDSN != "")
	}

	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	c := &Client{prefix: strings.ToLower(opt.Prefix), logf: logf}

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	for _, spec := range []struct {
		name string
		dsn  string
		dst  **sql.DB
	}{
		{"auth", opt.AuthDSN, &c.auth},
		{"characters", opt.CharDSN, &c.char},
		{"world", opt.WorldDSN, &c.world},
	} {
		db, err := sql.Open("mysql", spec.dsn)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("wowdata: open %s database: %w", spec.name, err)
		}
		*spec.dst = db
		if err := db.PingContext(ctx); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("wowdata: %s database did not answer (check the DSN in the environment): %w", spec.name, err)
		}
	}

	logf("wowdata: connected to auth, characters and world databases (prefix %q)", opt.Prefix)
	return c, nil
}

// Close closes every open handle and reports the first failure.
func (c *Client) Close() error {
	var errs []error
	for _, db := range []*sql.DB{c.auth, c.char, c.world} {
		if db == nil {
			continue
		}
		if err := db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Prefix returns the bot prefix writes are checked against.
func (c *Client) Prefix() string { return c.prefix }

// Ping verifies all three handles still answer, for callers that hold a Client
// across a long run.
func (c *Client) Ping(ctx context.Context) error {
	for _, spec := range []struct {
		name string
		db   *sql.DB
	}{
		{"auth", c.auth},
		{"characters", c.char},
		{"world", c.world},
	} {
		if spec.db == nil {
			return fmt.Errorf("wowdata: %s database is not open", spec.name)
		}
		if err := spec.db.PingContext(ctx); err != nil {
			return fmt.Errorf("wowdata: ping %s database: %w", spec.name, err)
		}
	}
	return nil
}

// requireBotName is the guard every write goes through: it is what makes
// "never touch real player data" a property of the code instead of a promise.
func (c *Client) requireBotName(kind, name string) error {
	if strings.HasPrefix(strings.ToLower(name), c.prefix) {
		return nil
	}
	return fmt.Errorf("%w: refusing to change %s %q, which does not start with %q",
		ErrNotBotName, kind, name, c.prefix)
}

// Account is one provisioned bot account.
type Account struct {
	// ID is the account row id, the value the characters table stores.
	ID int

	// Username is the stored (upper-case) login name.
	Username string

	// Created reports whether this call inserted the row. false means an
	// existing account had its password refreshed.
	Created bool
}

// EnsureAccount creates the account or refreshes its password, and returns its
// row id. It is idempotent: running it twice leaves exactly one account row.
//
// Salt and verifier come from upstream's harness (e2eharness.ComputeSRP6) rather
// than from a second SRP6 implementation living here — the protocol's byte order
// is exactly the kind of detail that should exist once.
func (c *Client) EnsureAccount(ctx context.Context, username, password string) (Account, error) {
	if err := c.requireBotName("account", username); err != nil {
		return Account{}, err
	}
	if password == "" {
		return Account{}, errors.New("wowdata: refusing to provision an account with an empty password")
	}

	// Both the authserver check and the world socket use the upper-case name.
	name := strings.ToUpper(username)
	salt, verifier := e2eharness.ComputeSRP6(name, strings.ToUpper(password))

	var id int
	err := c.auth.QueryRowContext(ctx, `SELECT id FROM account WHERE username = ?`, name).Scan(&id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, execErr := c.auth.ExecContext(ctx,
			`INSERT INTO account (username, salt, verifier, expansion) VALUES (?, ?, ?, 2)`,
			name, salt, verifier)
		if execErr != nil {
			return Account{}, fmt.Errorf("wowdata: insert account %s: %w", name, execErr)
		}
		newID, idErr := res.LastInsertId()
		if idErr != nil {
			return Account{}, fmt.Errorf("wowdata: read id of new account %s: %w", name, idErr)
		}
		c.logf("wowdata: provisioned account %s (id %d)", name, newID)
		return Account{ID: int(newID), Username: name, Created: true}, nil

	case err != nil:
		return Account{}, fmt.Errorf("wowdata: look up account %s: %w", name, err)
	}

	if _, err := c.auth.ExecContext(ctx,
		`UPDATE account SET salt = ?, verifier = ?, expansion = 2 WHERE id = ?`, salt, verifier, id); err != nil {
		return Account{}, fmt.Errorf("wowdata: refresh account %s: %w", name, err)
	}
	c.logf("wowdata: account %s (id %d) already existed, password refreshed", name, id)
	return Account{ID: id, Username: name}, nil
}

// AccountID returns the id of a bot account; found=false means there is none.
func (c *Client) AccountID(ctx context.Context, username string) (id int, found bool, err error) {
	if err := c.requireBotName("account", username); err != nil {
		return 0, false, err
	}

	err = c.auth.QueryRowContext(ctx, `SELECT id FROM account WHERE username = ?`, strings.ToUpper(username)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("wowdata: look up account %s: %w", username, err)
	}
	return id, true, nil
}

// DeleteAccount removes a bot account and its access rows. It is idempotent:
// deleting an account that is already gone succeeds.
//
// It refuses while the account still owns characters (ErrAccountNotEmpty).
// Character rows are removed by the server when it handles CMSG_CHAR_DELETE, and
// a leftover row reappears under the next account provisioned with the same
// name — the cross-contamination the prefix rule exists to prevent.
func (c *Client) DeleteAccount(ctx context.Context, username string) error {
	id, found, err := c.AccountID(ctx, username)
	if err != nil {
		return err
	}
	if !found {
		c.logf("wowdata: account %s was already gone", strings.ToUpper(username))
		return nil
	}

	chars, err := c.CharactersOfAccount(ctx, id)
	if err != nil {
		return err
	}
	if len(chars) > 0 {
		names := make([]string, 0, len(chars))
		for _, ch := range chars {
			names = append(names, ch.Name)
		}
		return fmt.Errorf("%w: account %s still owns %s (delete the character in game first)",
			ErrAccountNotEmpty, strings.ToUpper(username), strings.Join(names, ", "))
	}

	if _, err := c.auth.ExecContext(ctx, `DELETE FROM account_access WHERE id = ?`, id); err != nil {
		return fmt.Errorf("wowdata: delete access rows of account %s: %w", username, err)
	}
	res, err := c.auth.ExecContext(ctx, `DELETE FROM account WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("wowdata: delete account %s: %w", username, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("wowdata: account %s disappeared between the lookup and the delete", username)
	}
	c.logf("wowdata: deleted account %s (id %d)", strings.ToUpper(username), id)
	return nil
}

// Character is one row of acore_characters.characters, narrowed to the columns
// this project reads.
type Character struct {
	GUID      uint64
	Name      string
	AccountID int
	Race      uint8
	Class     uint8
	Level     uint8
	Online    uint8
	Map       uint16
}

// characterColumns is the projection every character query shares.
const characterColumns = "guid, name, account, race, class, level, online, map"

// CharacterByName looks a character up by name. found=false means the account
// does not have it; it never means "create one" — creating is a protocol
// operation owned by internal/wow.
func (c *Client) CharacterByName(ctx context.Context, name string) (Character, bool, error) {
	row := c.char.QueryRowContext(ctx, `SELECT `+characterColumns+` FROM characters WHERE name = ?`, name)

	ch, err := scanCharacter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, false, nil
	}
	if err != nil {
		return Character{}, false, fmt.Errorf("wowdata: look up character %s: %w", name, err)
	}
	return ch, true, nil
}

// CharactersOfAccount lists a bot account's characters, oldest first.
func (c *Client) CharactersOfAccount(ctx context.Context, accountID int) ([]Character, error) {
	rows, err := c.char.QueryContext(ctx,
		`SELECT `+characterColumns+` FROM characters WHERE account = ? ORDER BY guid`, accountID)
	if err != nil {
		return nil, fmt.Errorf("wowdata: list characters of account %d: %w", accountID, err)
	}
	defer rows.Close()

	var out []Character
	for rows.Next() {
		ch, err := scanCharacter(rows)
		if err != nil {
			return nil, fmt.Errorf("wowdata: scan character of account %d: %w", accountID, err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wowdata: iterate characters of account %d: %w", accountID, err)
	}
	return out, nil
}

// scanner is the shape *sql.Row and *sql.Rows share.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanCharacter reads the characterColumns projection.
func scanCharacter(s scanner) (Character, error) {
	var ch Character
	err := s.Scan(&ch.GUID, &ch.Name, &ch.AccountID, &ch.Race, &ch.Class, &ch.Level, &ch.Online, &ch.Map)
	return ch, err
}
