package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"azerothcore-autobot/internal/config"
	"azerothcore-autobot/internal/wowdata"
)

// runAccountList prints the accounts under the prefix.
//
// It needs no password, which is the reason the password and the DSNs are
// resolved separately: "which accounts exist" should never be blocked on a secret
// the command does not use.
func runAccountList(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("account list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)
	asJSON := fs.Bool("json", false, "以 JSON 输出，便于脚本处理")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadAccountConfig(base, "")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	rows, err := data.ListAccounts(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("account: encode listing: %w", err)
		}
		return nil
	}

	printAccounts(os.Stdout, data.Prefix(), rows)
	logger.Info("account: listed", "count", len(rows), "prefix", data.Prefix())
	return nil
}

// printAccounts writes the listing as a fixed-width table. stdout carries only
// what a caller may want to parse; progress lines go to the logger on stderr.
func printAccounts(w io.Writer, prefix string, rows []wowdata.AccountRow) {
	if len(rows) == 0 {
		fmt.Fprintf(w, "no accounts with prefix %q\n", prefix)
		return
	}
	fmt.Fprintf(w, "%-6s %-22s %-10s %-4s %-7s %s\n", "ID", "USERNAME", "EXPANSION", "GM", "ONLINE", "LAST LOGIN")
	for _, row := range rows {
		last := row.LastLogin
		if last == "" {
			last = "never"
		}
		fmt.Fprintf(w, "%-6d %-22s %-10d %-4d %-7t %s\n",
			row.ID, row.Username, row.Expansion, row.GMLevel, row.Online, last)
	}
}

// loadAccountConfig resolves the configuration for the account commands.
//
// It is loadConfig's chain — profile, environment, flags, then validation — with
// one difference: the password is optional. Listing and deleting accounts need no
// password at all, and creating one can take it from --password, so these commands
// must not fail because a secret they were never going to use is unset. An
// explicit --password wins over the environment, since the flag is what the
// operator just typed.
func loadAccountConfig(f *baseFlags, password string) (*config.Config, error) {
	cfg, err := config.Load(f.profile)
	if err != nil {
		return nil, err
	}
	if err := f.apply(cfg); err != nil {
		return nil, err
	}
	if password != "" {
		cfg.Account.Password = password
	} else if err := cfg.ResolvePasswordSecret(os.LookupEnv); err != nil {
		return nil, err
	}
	if err := cfg.ResolveDatabaseSecrets(os.LookupEnv); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
