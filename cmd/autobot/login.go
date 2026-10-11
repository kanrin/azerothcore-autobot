package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"
)

// runLogin is milestone 1 end to end: make sure the account exists, let the
// protocol layer create or select the character, wait until the session is in
// the world, check the result against the database — the client's object cache
// is never the authority — and log out again so no ghost session is left behind.
func runLogin(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(base)
	if err != nil {
		return err
	}
	if err := requireAccountName(cfg); err != nil {
		return err
	}
	if cfg.Character.Name == "" {
		return fmt.Errorf("autobot: no character name; set character.name in the profile, " +
			"AUTOBOT_CHARACTER_NAME in the environment, or pass --char-name")
	}

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	logger.Info("login: configuration", "profile", orNone(cfg.Path), "auth", cfg.AuthAddr,
		"realm", cfg.RealmIndex, "account", cfg.Account.Username, "character", cfg.Character.Name,
		"race", cfg.Character.Race, "class", cfg.Character.Class)

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	acct, err := data.EnsureAccount(ctx, cfg.Account.Username, cfg.Account.Password)
	if err != nil {
		return err
	}
	logger.Info("login: account ready", "username", acct.Username, "id", acct.ID, "created", acct.Created)

	sess, err := wow.Dial(ctx, connectOptions(cfg, logger))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer sess.Close()

	// Race drives the chat language of GM commands; set it before anything that
	// might speak.
	sess.SetCharRace(cfg.Character.Race)

	char, err := sess.EnterWorld(ctx, wow.EnterWorldOptions{
		Name:       cfg.Character.Name,
		Race:       cfg.Character.Race,
		Class:      cfg.Character.Class,
		Appearance: wow.DefaultAppearance(cfg.Character.Gender),
		Logf:       logf(logger),
	})
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	x, y, z, o, mapID := sess.Position()
	logger.Info("login: in world", "character", char.Name, "guid", char.GUID, "level", char.Level,
		"map", mapID, "x", x, "y", y, "z", z, "orientation", o,
		"health", sess.Health(), "max_health", sess.MaxHealth(), "world_addr", sess.RealmAddress())

	row, found, err := data.CharacterByName(ctx, char.Name)
	switch {
	case err != nil:
		return fmt.Errorf("login: verify character in the database: %w", err)
	case !found:
		return fmt.Errorf("login: %s is in the session but has no row in the characters database", char.Name)
	case row.GUID != char.GUID:
		return fmt.Errorf("login: session guid %d does not match database guid %d", char.GUID, row.GUID)
	}
	logger.Info("login: database agrees", "guid", row.GUID, "account_id", row.AccountID,
		"race", row.Race, "class", row.Class, "level", row.Level, "map", row.Map)

	// A failed logout is a failure, not a warning: the server keeps the session
	// alive for a while, and the next login of the same account would then be
	// refused as "already in world".
	if err := sess.Logout(15 * time.Second); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	logger.Info("login: OK", "character", char.Name, "guid", char.GUID)
	return nil
}
