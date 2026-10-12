package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"azerothcore-autobot/internal/bot"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"
)

// OpsOptions tunes the operations. The zero value is usable.
type OpsOptions struct {
	// Characters also creates each bot's character. That is a protocol operation,
	// so it logs every bot in once and out again — paced by the policy, because
	// the authserver is single-threaded.
	Characters bool

	// Logger receives progress lines. Nil discards them.
	Logger *slog.Logger

	// Logf receives the AzerothGhost client's own lines.
	Logf func(format string, args ...interface{})

	// Timeout bounds one bot's login, character creation and logout together.
	Timeout time.Duration
}

// withDefaults fills the zero fields.
func (o OpsOptions) withDefaults() OpsOptions {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	return o
}

// BotReport is what one operation did to one bot.
type BotReport struct {
	Name      string
	Account   string
	Character string
	AccountID int

	// AccountCreated reports whether this run inserted the account row.
	AccountCreated bool

	// CharacterCreated reports whether the character had to be created.
	CharacterCreated bool

	// Action is what happened, in one word or two: "provisioned", "provisioned+
	// character", "character deleted", "absent".
	Action string
}

// Report is the outcome of an operation over a roster.
type Report struct {
	Bots []BotReport

	// Errors are the per-bot failures. An operation keeps going after one, because
	// failing to reach the third bot should not undo the first two.
	Errors []error
}

// Failed reports whether any bot failed.
func (r Report) Failed() bool { return len(r.Errors) > 0 }

// Err joins the per-bot errors.
func (r Report) Err() error { return errors.Join(r.Errors...) }

// Provision creates the accounts, and optionally the characters, of the roster.
//
// It is idempotent: an existing account has its password refreshed rather than
// being duplicated, and a character that is already there is left alone. Running
// it twice must leave the row counts unchanged, which is what makes it safe to
// call before every run.
func Provision(ctx context.Context, f *Fleet, data *wowdata.Client, password string, opts OpsOptions) (Report, error) {
	opts = opts.withDefaults()

	report := Report{}
	for i, b := range f.Bots {
		if i > 0 {
			// Pace the logins: the authserver reads the logon proof on one thread,
			// and a burst of them times out.
			if err := sleepCtx(ctx, f.Policy.Login.Stagger); err != nil {
				return report, err
			}
		}

		entry := BotReport{Name: b.Name, Account: b.Account, Character: b.Character}

		acct, err := data.EnsureAccount(ctx, b.Account, password)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
			report.Bots = append(report.Bots, entry)
			continue
		}
		entry.AccountID, entry.AccountCreated = acct.ID, acct.Created
		entry.Action = "provisioned"

		if opts.Characters {
			created, err := ensureCharacter(ctx, f, b, password, opts)
			if err != nil {
				report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
				report.Bots = append(report.Bots, entry)
				continue
			}
			entry.CharacterCreated = created
			if created {
				entry.Action = "provisioned+character"
			}
		}

		opts.Logger.Info("fleet: bot provisioned", "bot", b.Name, "account", acct.Username,
			"account_id", acct.ID, "account_created", acct.Created, "character", b.Character,
			"character_created", entry.CharacterCreated)
		report.Bots = append(report.Bots, entry)
	}
	return report, nil
}

// ensureCharacter logs one bot in so the server creates its character, then logs
// out again. Creating a character is a protocol operation, so there is no
// data-layer shortcut for it.
func ensureCharacter(ctx context.Context, f *Fleet, b Bot, password string, opts OpsOptions) (bool, error) {
	charCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	runner, err := bot.New(specFor(f, b, password, b.FeaturesFor(f.Policy)), bot.Options{
		Logf:         opts.Logf,
		Logger:       opts.Logger,
		LoginTimeout: opts.Timeout,
	})
	if err != nil {
		return false, err
	}

	if err := runner.Login(charCtx, nil); err != nil {
		return false, err
	}
	// EnterWorld creates the character when the account does not have it, so the
	// interesting fact is whether it had to.
	created := runner.CharacterCreated()

	if err := runner.Close(charCtx); err != nil {
		return created, err
	}
	return created, nil
}

// sleepCtx waits for d, or returns early when the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// StatusRow is one bot as the databases see it.
type StatusRow struct {
	Name      string  `json:"name"`
	Account   string  `json:"account"`
	Character string  `json:"character"`
	Faction   Faction `json:"-"`

	FactionName string `json:"faction"`

	// AccountID is 0 when the account does not exist.
	AccountID  int  `json:"account_id"`
	HasAccount bool `json:"account_exists"`

	// CharacterGUID is 0 when the character does not exist.
	CharacterGUID uint64 `json:"character_guid"`
	HasCharacter  bool   `json:"character_exists"`

	Level  uint8  `json:"level"`
	Map    uint16 `json:"map"`
	Online uint8  `json:"online"`
	Error  string `json:"error,omitempty"`
}

// Status reads the roster's state from the databases.
//
// The databases are the authority: the client's object cache only knows about the
// bots in this process, and a fleet's job is to answer "what exists" and "is it
// balanced" for all of them, including the ones that are not running.
func Status(ctx context.Context, f *Fleet, data *wowdata.Client) ([]StatusRow, error) {
	rows := make([]StatusRow, 0, len(f.Bots))

	for _, b := range f.Bots {
		row := StatusRow{Name: b.Name, Account: b.Account, Character: b.Character}
		if faction, err := b.Faction(); err == nil {
			row.Faction, row.FactionName = faction, faction.String()
		}

		id, found, err := data.AccountID(ctx, b.Account)
		switch {
		case err != nil:
			row.Error = err.Error()
		case found:
			row.AccountID, row.HasAccount = id, true
		}

		char, found, err := data.CharacterByName(ctx, b.Character)
		switch {
		case err != nil:
			row.Error = errors.Join(errorOrNil(row.Error), err).Error()
		case found:
			row.CharacterGUID, row.HasCharacter = char.GUID, true
			row.Level, row.Map, row.Online = char.Level, char.Map, char.Online
		}

		rows = append(rows, row)
	}
	return rows, nil
}

// errorOrNil turns a non-empty message back into an error for joining.
func errorOrNil(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

// GC takes the roster's data away again: the character through the protocol, the
// account through the data layer. It is idempotent — a bot that is already gone
// is reported as absent, not as a failure.
//
// Deleting the character first is not a preference: an account that still owns
// characters is refused, because those rows would reappear under the next account
// provisioned with the same name.
func GC(ctx context.Context, f *Fleet, data *wowdata.Client, password string, opts OpsOptions) (Report, error) {
	opts = opts.withDefaults()

	report := Report{}
	for i, b := range f.Bots {
		if i > 0 {
			if err := sleepCtx(ctx, f.Policy.Login.Stagger); err != nil {
				return report, err
			}
		}

		entry := BotReport{Name: b.Name, Account: b.Account, Character: b.Character, Action: "absent"}

		char, charFound, err := data.CharacterByName(ctx, b.Character)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
			report.Bots = append(report.Bots, entry)
			continue
		}
		_, accountFound, err := data.AccountID(ctx, b.Account)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
			report.Bots = append(report.Bots, entry)
			continue
		}
		if !charFound && !accountFound {
			// Nothing to do. Reporting "absent" rather than "deleted" is what makes
			// running gc twice visibly safe: the second run says so.
			opts.Logger.Info("fleet: bot was already gone", "bot", b.Name, "account", b.Account,
				"character", b.Character)
			report.Bots = append(report.Bots, entry)
			continue
		}

		if charFound {
			if err := deleteCharacter(ctx, f, b, char.GUID, password, opts); err != nil {
				report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
				report.Bots = append(report.Bots, entry)
				continue
			}
			// The row has to be gone before the account goes: SMSG_CHAR_DELETE is
			// answered before the deletion is committed, and the account delete
			// after it would be refused with "still owns characters" — which is
			// what the first live gc run did for its very first bot.
			if err := waitCharacterGone(ctx, data, b.Character, opts.Timeout); err != nil {
				report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
				report.Bots = append(report.Bots, entry)
				continue
			}
			entry.Action = "character deleted"
		}

		if err := data.DeleteAccount(ctx, b.Account); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("%s: %w", b.Name, err))
			report.Bots = append(report.Bots, entry)
			continue
		}
		switch {
		case charFound && accountFound:
			entry.Action = "character+account deleted"
		case accountFound:
			entry.Action = "account deleted"
		}

		opts.Logger.Info("fleet: bot removed", "bot", b.Name, "account", b.Account,
			"character", b.Character, "had_character", charFound, "had_account", accountFound)
		report.Bots = append(report.Bots, entry)
	}
	return report, nil
}

// waitCharacterGone polls until the server's deletion of a character has landed in
// the database.
//
// The packet is not the fact: AC answers CMSG_CHAR_DELETE and commits the row
// removal separately, so an account delete issued right after the reply can still
// see the character and be refused. Waiting on the row is waiting on the real
// signal; a sleep would be a guess, and a short one would make the failure
// intermittent.
func waitCharacterGone(ctx context.Context, data *wowdata.Client, name string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		_, found, err := data.CharacterByName(ctx, name)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("fleet: character %s is still in the database %s after SMSG_CHAR_DELETE",
				name, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// deleteCharacter removes one character through the protocol.
//
// The character-select phase is enough: the server refuses to delete a character
// that is still logged in, and this runs before any world login.
func deleteCharacter(ctx context.Context, f *Fleet, b Bot, guid uint64, password string, opts OpsOptions) error {
	delCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	sess, err := wow.Dial(delCtx, wow.ConnectOptions{
		AuthAddr:   f.Policy.AuthAddr,
		Username:   b.Account,
		Password:   password,
		RealmIndex: f.Policy.RealmIndex,
		Logf:       opts.Logf,
	})
	if err != nil {
		return err
	}
	defer sess.Close()

	return sess.DeleteCharacter(delCtx, guid, 30*time.Second)
}

// specFor turns a roster entry into the spec one runner needs.
func specFor(f *Fleet, b Bot, password string, features Features) bot.Spec {
	return bot.Spec{
		Name:       b.Name,
		AuthAddr:   f.Policy.AuthAddr,
		RealmIndex: f.Policy.RealmIndex,
		Account:    b.Account,
		Password:   password,
		Character:  b.Character,
		Race:       b.Race,
		Class:      b.Class,
		Gender:     b.Gender,
		DataDir:    f.Policy.DataDir,
		Features:   featuresFrom(features),
	}
}

// featuresFrom converts the fleet's feature switches into the runner's.
func featuresFrom(f Features) bot.Features {
	return bot.Features{Travel: f.Travel}
}
