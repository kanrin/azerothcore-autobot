package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"azerothcore-autobot/internal/wowdata"
)

// runAccount provisions, lists and removes accounts in the bot namespace.
//
// The prefix is the project's safety boundary: every name here has to start with
// it, taken from --prefix, account.prefix in the profile or
// AUTOBOT_ACCOUNT_PREFIX. That is what keeps a mistyped command from reaching a
// real player's account. Naming an account outside the convention is therefore
// not a switch that turns a check off — it is a statement that the name belongs
// to us, which is why --prefix exists instead of an "ignore the prefix" flag.
func runAccount(args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return fmt.Errorf("autobot: account needs a subcommand: create, list or delete")
	}
	switch sub := args[0]; sub {
	case "create":
		return runAccountCreate(args[1:], logger)
	case "list":
		return runAccountList(args[1:], logger)
	case "delete":
		return runAccountDelete(args[1:], logger)
	default:
		return fmt.Errorf("autobot: unknown account subcommand %q (want create, list or delete)", sub)
	}
}

// runAccountCreate creates the account, or refreshes its password when it already
// exists. It is idempotent: running it twice leaves exactly one account row.
func runAccountCreate(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("account create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)
	password := fs.String("password", "",
		"账号密码；不给则从 AUTOBOT_PASSWORD（或 profile 的 account.password_env）读取。"+
			"写在命令行会留在 shell history 里")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadAccountConfig(base, *password)
	if err != nil {
		return err
	}
	if err := requireAccountName(cfg); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	acct, err := data.EnsureAccount(ctx, cfg.Account.Username, cfg.Account.Password)
	if err != nil {
		return err
	}

	action := "created"
	if !acct.Created {
		action = "password refreshed"
	}
	fmt.Printf("account %s %s (id %d, prefix %q)\n", acct.Username, action, acct.ID, data.Prefix())
	logger.Info("account: OK", "action", action, "username", acct.Username, "id", acct.ID)
	return nil
}

// runAccountDelete removes the account. An account that still owns characters is
// refused: deleting it would leave those rows behind to reappear under the next
// account provisioned with the same name, and characters are removed through the
// protocol (in game) rather than by editing the database.
func runAccountDelete(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("account delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadAccountConfig(base, "")
	if err != nil {
		return err
	}
	if err := requireAccountName(cfg); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	if err := data.DeleteAccount(ctx, cfg.Account.Username); err != nil {
		return err
	}
	fmt.Printf("account %s deleted\n", cfg.Account.Username)
	return nil
}
