package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"
)

// runProbe answers one question: how far does a session get?
//
// It provisions the account, authenticates, opens the world socket and stops.
// No character is created, which makes it the cheapest way to separate a
// server-side precondition problem (Warden, realmlist, closed world) from a
// character-select one.
//
// Exit code 0 means PhaseAuthed was reached; otherwise the stage that failed is
// printed, which is the evidence the failure taxonomy asks for before touching
// anything else.
func runProbe(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
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

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	logger.Info("probe: configuration", "profile", orNone(cfg.Path), "auth", cfg.AuthAddr,
		"realm", cfg.RealmIndex, "account", cfg.Account.Username, "prefix", cfg.Account.Prefix)

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	acct, err := data.EnsureAccount(ctx, cfg.Account.Username, cfg.Account.Password)
	if err != nil {
		return err
	}
	logger.Info("probe: account ready", "username", acct.Username, "id", acct.ID, "created", acct.Created)

	sess, err := wow.Dial(ctx, connectOptions(cfg, logger))
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	defer sess.Close()

	logger.Info("probe: OK", "phase", sess.Phase().String(), "world_addr", sess.RealmAddress())
	return nil
}

// orNone renders an empty string as "none" instead of an empty log value.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
