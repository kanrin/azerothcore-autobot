package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"azerothcore-autobot/internal/config"
	"azerothcore-autobot/internal/fleet"
	"azerothcore-autobot/internal/wowdata"
)

// runFleet is milestone 2: it owns fleet.yml and dispatches the roster
// operations. The roster is the unit of work — one command provisions, inspects,
// removes or runs every bot it lists.
func runFleet(args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return fmt.Errorf("autobot: fleet needs a subcommand: status, provision, gc or run")
	}
	switch sub := args[0]; sub {
	case "status":
		return runFleetStatus(args[1:], logger)
	case "provision":
		return runFleetProvision(args[1:], logger)
	case "gc":
		return runFleetGC(args[1:], logger)
	case "run":
		return runFleetRun(args[1:], logger)
	default:
		return fmt.Errorf("autobot: unknown fleet subcommand %q (want status, provision, gc or run)", sub)
	}
}

// fleetFlags are the flags every fleet subcommand shares.
type fleetFlags struct {
	path       string
	asJSON     bool
	characters bool
	timeout    time.Duration
	runFor     time.Duration
}

// newFleetFlags registers the fleet flags.
func newFleetFlags(fs *flag.FlagSet) *fleetFlags {
	f := &fleetFlags{}
	fs.StringVar(&f.path, "fleet", defaultFleetPath(),
		"fleet.yml 路径（默认 $AUTOBOT_FLEET 或 ./fleet.yml）")
	fs.BoolVar(&f.asJSON, "json", false, "以 JSON 输出（status）")
	fs.BoolVar(&f.characters, "characters", false, "provision 时同时用协议登录建号")
	fs.DurationVar(&f.timeout, "timeout", 5*time.Minute, "整个操作的时限")
	fs.DurationVar(&f.runFor, "for", 0, "run 的时长（0 = 一直跑到 Ctrl-C）")
	return f
}

// defaultFleetPath is the fleet file used when --fleet is absent.
func defaultFleetPath() string {
	if v := os.Getenv("AUTOBOT_FLEET"); v != "" {
		return v
	}
	return "fleet.yml"
}

// load reads the fleet file and overlays the environment, which is how one
// fleet.yml works from a laptop and from the server's own host without editing.
func (f *fleetFlags) load(logger *slog.Logger) (*fleet.Fleet, error) {
	fl, err := fleet.Load(f.path)
	if err != nil {
		return nil, err
	}
	if err := fl.ApplyEnv(os.LookupEnv); err != nil {
		return nil, err
	}
	logger.Info("fleet: loaded", "file", fl.Path, "bots", len(fl.Bots),
		"roster", fl.RosterFiles, "prefix", fl.Policy.Prefix)
	return fl, nil
}

// openData opens the data layer with the fleet's prefix as the guard.
//
// The DSNs come from the environment, not from the fleet file: a fleet file
// describes bots, and secrets stay in the environment where a profile keeps them
// too.
func openFleetData(fl *fleet.Fleet, logger *slog.Logger) (*wowdata.Client, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, err
	}
	if err := cfg.ResolveDatabaseSecrets(os.LookupEnv); err != nil {
		return nil, err
	}
	return wowdata.Open(wowdata.Options{
		AuthDSN:  cfg.Database.AuthDSN,
		CharDSN:  cfg.Database.CharDSN,
		WorldDSN: cfg.Database.WorldDSN,
		Prefix:   fl.Policy.Prefix,
		Logf:     logf(logger),
	})
}

// runFleetStatus reports what the roster looks like in the databases, and whether
// its factions are balanced.
func runFleetStatus(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("fleet status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	flags := newFleetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	fl, err := flags.load(logger)
	if err != nil {
		return err
	}
	data, err := openFleetData(fl, logger)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), flags.timeout)
	defer cancel()

	rows, err := fleet.Status(ctx, fl, data)
	if err != nil {
		return err
	}

	if flags.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return err
		}
	} else {
		printFleetStatus(os.Stdout, rows)
	}

	split := fleet.SplitOf(fl.Bots)
	balanced := split.Within(fl.Policy.Balance.Tolerance)
	logger.Info("fleet: status", "bots", len(rows), "split", split.String(),
		"tolerance", fl.Policy.Balance.Tolerance, "balanced", balanced)

	if !balanced && fl.Policy.Balance.Enforce {
		return fmt.Errorf("fleet: %s is outside tolerance %.2f", split.String(), fl.Policy.Balance.Tolerance)
	}
	return nil
}

// printFleetStatus writes the roster as a table.
func printFleetStatus(w io.Writer, rows []fleet.StatusRow) {
	fmt.Fprintf(w, "%-10s %-12s %-14s %-9s %-6s %-6s %-5s %s\n",
		"BOT", "ACCOUNT", "CHARACTER", "FACTION", "ACC", "CHAR", "ONLINE", "LEVEL")

	for _, r := range rows {
		account, character := "no", "no"
		if r.HasAccount {
			account = fmt.Sprintf("%d", r.AccountID)
		}
		if r.HasCharacter {
			character = fmt.Sprintf("%d", r.CharacterGUID)
		}
		online := "-"
		if r.HasCharacter {
			if r.Online != 0 {
				online = "yes"
			} else {
				online = "no"
			}
		}
		level := "-"
		if r.HasCharacter {
			level = fmt.Sprintf("%d", r.Level)
		}

		fmt.Fprintf(w, "%-10s %-12s %-14s %-9s %-6s %-6s %-5s %s\n",
			r.Name, r.Account, r.Character, r.FactionName, account, character, online, level)
		if r.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", r.Error)
		}
	}
}

// runFleetProvision creates the roster's accounts and, with --characters, their
// characters. It is the idempotent half of the lifecycle: running it twice leaves
// the row counts unchanged.
func runFleetProvision(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("fleet provision", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	flags := newFleetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	fl, err := flags.load(logger)
	if err != nil {
		return err
	}
	password, err := fl.Password(os.LookupEnv)
	if err != nil {
		return err
	}
	data, err := openFleetData(fl, logger)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), flags.timeout)
	defer cancel()

	report, err := fleet.Provision(ctx, fl, data, password, fleet.OpsOptions{
		Characters: flags.characters,
		Logger:     logger,
		Logf:       logf(logger),
		Timeout:    flags.timeout,
	})
	if err != nil {
		return err
	}

	for _, b := range report.Bots {
		fmt.Printf("%-10s %-12s %-14s %s\n", b.Name, b.Account, b.Character, b.Action)
	}
	logger.Info("fleet: provisioned", "bots", len(report.Bots), "characters", flags.characters,
		"failed", len(report.Errors))
	if report.Failed() {
		return report.Err()
	}
	return nil
}

// runFleetGC takes the roster's characters and accounts away again.
func runFleetGC(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("fleet gc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	flags := newFleetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	fl, err := flags.load(logger)
	if err != nil {
		return err
	}
	password, err := fl.Password(os.LookupEnv)
	if err != nil {
		return err
	}
	data, err := openFleetData(fl, logger)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), flags.timeout)
	defer cancel()

	report, err := fleet.GC(ctx, fl, data, password, fleet.OpsOptions{
		Logger:  logger,
		Logf:    logf(logger),
		Timeout: flags.timeout,
	})
	if err != nil {
		return err
	}

	for _, b := range report.Bots {
		fmt.Printf("%-10s %-12s %-14s %s\n", b.Name, b.Account, b.Character, b.Action)
	}
	logger.Info("fleet: collected", "bots", len(report.Bots), "failed", len(report.Errors))
	if report.Failed() {
		return report.Err()
	}
	return nil
}

// runFleetRun keeps the whole roster online until Ctrl-C, then stops every bot
// gracefully.
func runFleetRun(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("fleet run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	flags := newFleetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	fl, err := flags.load(logger)
	if err != nil {
		return err
	}
	password, err := fl.Password(os.LookupEnv)
	if err != nil {
		return err
	}
	data, err := openFleetData(fl, logger)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("fleet: starting", "bots", len(fl.Bots), "split", fleet.SplitOf(fl.Bots).String(),
		"stagger", fl.Policy.Login.Stagger.String(),
		"max_concurrent", fl.Policy.Login.MaxConcurrent)

	outcome, err := fleet.Supervise(ctx, fl, data, password, fleet.RunOptions{
		OpsOptions: fleet.OpsOptions{Logger: logger, Logf: logf(logger), Timeout: flags.timeout},
		For:        flags.runFor,
	})
	if err != nil {
		return err
	}

	stamp := time.Now().Format("2006-01-02T15:04:05Z07:00")
	fmt.Printf("fleet run %s: %d bots, uptime %s, %d clean\n", stamp, len(outcome.Bots),
		outcome.Uptime().Round(time.Second), outcome.Clean())
	for _, b := range outcome.Bots {
		fmt.Printf("  %-10s logins=%d sessions=%d ticks=%d errors=%d\n",
			b.Name, b.Logins, b.Sessions, b.Ticks, len(b.Errors))
	}

	logger.Info("fleet: run finished", "uptime", outcome.Uptime().Round(time.Second).String(),
		"clean", outcome.Clean(), "bots", len(outcome.Bots))
	if err := outcome.Err(); err != nil {
		return err
	}
	return nil
}
