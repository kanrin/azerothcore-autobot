package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"azerothcore-autobot/internal/agent"
	"azerothcore-autobot/internal/bot"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"
)

// runRun is milestone 1's finish line and the base every later milestone builds
// on: configuration, login, the agent loop, and a logout that leaves nothing
// behind.
//
// It stays resident until it is told to stop (Ctrl-C, --for, or --until-arrival).
// Stopping is a graceful shutdown, not a killed process: the running behavior is
// released, the character is logged out, and only then is the socket closed.
func runRun(args []string, logger *slog.Logger) (err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)

	features := fs.String("features", "travel",
		"启用的行为，逗号分隔：travel（走路）、quest（找 NPC 接任务）；空字符串=不启用")
	gotoSpec := fs.String("goto", "",
		"登录后走过去的目标：x,y,z 或 map:x,y,z（例如 0:-8902.59,-162.61,82.02）")
	untilArrival := fs.Bool("until-arrival", false, "走到 --goto 之后自动优雅退出（需要 --goto）")
	questGiver := fs.Uint("quest-giver", 0,
		"任务发布者的 creature entry；0 = 自动挑附近任意有任务的 NPC")
	untilQuest := fs.Uint("until-quest", 0,
		"把该任务接进任务日志后自动优雅退出（需要 --features quest）")
	runFor := fs.Duration("for", 0, "最多运行多久后优雅退出（0 = 一直跑，直到 Ctrl-C）")
	untilTimeout := fs.Duration("until-timeout", 5*time.Minute, "等 --until-arrival / --until-quest 的上限")
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
	if err := requireCharName(cfg); err != nil {
		return err
	}

	var point bot.Point
	if *gotoSpec != "" {
		if point, err = bot.ParsePoint(*gotoSpec); err != nil {
			return err
		}
	} else if *untilArrival {
		return fmt.Errorf("autobot: --until-arrival needs --goto")
	}

	specFeatures, err := parseFeatures(*features)
	if err != nil {
		return err
	}
	if specFeatures.Travel && cfg.DataDir == "" {
		return fmt.Errorf("autobot: travel is enabled but data_dir is empty; " +
			"set it in the profile or AUTOBOT_DATA_DIR (maps/, mmaps/ and vmaps/ live there)")
	}
	if (*untilQuest != 0 || *questGiver != 0) && !specFeatures.Quest {
		return fmt.Errorf("autobot: --until-quest and --quest-giver need --features quest")
	}
	if *untilArrival && *untilQuest != 0 {
		return fmt.Errorf("autobot: --until-arrival and --until-quest cannot both stop the run")
	}

	spec := bot.Spec{
		Name:       cfg.Character.Name,
		AuthAddr:   cfg.AuthAddr,
		RealmIndex: cfg.RealmIndex,
		Account:    cfg.Account.Username,
		Password:   cfg.Account.Password,
		Character:  cfg.Character.Name,
		Race:       cfg.Character.Race,
		Class:      cfg.Character.Class,
		Gender:     cfg.Character.Gender,
		DataDir:    cfg.DataDir,
		QuestGiver: uint32(*questGiver),
		Features:   specFeatures,
	}

	// Ctrl-C and SIGTERM are the stop signal: they cancel the context, which ends
	// the agent loop and reaches the logout below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The data layer is opened before the runner because the features read world
	// knowledge through it (which quests an NPC offers, what a quest requires).
	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	runner, err := bot.New(spec, bot.Options{
		Tick:   200 * time.Millisecond,
		Logf:   logf(logger),
		Logger: logger,
		World:  data,
	})
	if err != nil {
		return err
	}

	logger.Info("run: starting", "profile", orNone(cfg.Path), "account", cfg.Account.Username,
		"character", cfg.Character.Name, "auth", cfg.AuthAddr, "features", *features,
		"data_dir", orNone(cfg.DataDir))

	if err := runner.Login(ctx, data); err != nil {
		return err
	}

	// From here on the session is live, so every exit path has to log out — the
	// error paths included. A ghost session keeps the server thinking the
	// character is in the world and refuses the account's next login.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if closeErr := runner.Close(closeCtx); closeErr != nil && err == nil {
			err = fmt.Errorf("run: %w", closeErr)
		}
	}()

	if *gotoSpec != "" {
		self, ok := bot.SelfOf(runner.Blackboard())
		if !ok {
			return fmt.Errorf("autobot: no self state after login")
		}
		dest := point.Destination(self.MapID, "run --goto "+point.String())
		logger.Info("run: destination", "dest", dest.String(), "map", self.MapID)
		bot.GoTo(runner.Blackboard(), dest)
	}

	// The run ends when the context is cancelled, the session dies, or --for expires;
	// --until-arrival and --until-quest add "the thing I asked for happened" to that
	// list.
	playCtx := ctx
	if *runFor > 0 {
		var cancel context.CancelFunc
		playCtx, cancel = context.WithTimeout(ctx, *runFor)
		defer cancel()
	}
	if *untilArrival || *untilQuest != 0 {
		var cancel context.CancelFunc
		playCtx, cancel = context.WithCancel(playCtx)
		defer cancel()
		if *untilArrival {
			go waitForArrival(playCtx, cancel, runner.Blackboard(), *untilTimeout, logger)
		}
		if *untilQuest != 0 {
			go waitForQuest(playCtx, cancel, runner.Session(), uint32(*untilQuest), *untilTimeout, logger)
		}
	}

	playErr := runner.Play(playCtx)
	logger.Info("run: loop stopped", "reason", stopReason(playCtx, ctx))

	if playErr != nil {
		return fmt.Errorf("run: %w", playErr)
	}

	logger.Info("run: stopped", "character", cfg.Character.Name)
	return nil
}

// parseFeatures turns the --features list into the switches the runner takes.
//
// An unknown name is an error rather than a silent no-op: a bot that is supposed
// to walk but stands still because of a typo is exactly the kind of thing that
// costs an hour of debugging.
func parseFeatures(list string) (bot.Features, error) {
	var features bot.Features

	for _, raw := range strings.Split(list, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		switch name {
		case "":
			continue
		case "travel", "walk":
			features.Travel = true
		case "quest":
			features.Quest = true
		default:
			return features, fmt.Errorf("autobot: unknown feature %q (known: travel, quest)", raw)
		}
	}
	return features, nil
}

// waitForArrival ends the run once the pending walk is finished, by arriving or
// by giving up, and says which it was.
//
// The poll waits on a real signal — the arrival record the travel behavior writes
// — rather than sleeping for a fixed time, because how long a walk takes depends
// on the route and the server's position updates.
func waitForArrival(ctx context.Context, cancel context.CancelFunc, bb *agent.Blackboard, timeout time.Duration, logger *slog.Logger) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			logger.Warn("run: the walk did not finish in time", "timeout", timeout.String())
			cancel()
			return
		case <-ticker.C:
			if arrival, ok := bot.LastArrival(bb); ok {
				logger.Info("run: arrival confirmed, stopping", "dest", arrival.Destination.String())
				cancel()
				return
			}
			if _, ok := bot.DestinationOf(bb); !ok {
				logger.Warn("run: the walk request is gone without an arrival (the bot gave up), stopping")
				cancel()
				return
			}
		}
	}
}

// stopReason explains why the loop ended, so the log answers "why did it stop"
// waitForQuest ends the run once the quest is in the bot's quest log.
//
// The log is the signal, not a packet: accepting is confirmed by the log changing
// (see wow.Session.AcceptQuest), and the log is what a client displays. Polling a real
// condition is the point; sleeping for "long enough" would either be too short or
// waste the run.
func waitForQuest(ctx context.Context, cancel context.CancelFunc, sess *wow.Session,
	questID uint32, timeout time.Duration, logger *slog.Logger) {

	if sess == nil {
		return
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			logger.Warn("run: the quest never appeared in the log",
				"quest", questID, "timeout", timeout.String())
			cancel()
			return
		case <-ticker.C:
			for _, slot := range sess.QuestLog() {
				if slot.QuestID == questID {
					logger.Info("run: quest is in the log, stopping",
						"quest", questID, "slot", slot.Index, "state", slot.State)
					cancel()
					return
				}
			}
		}
	}
}

// stopReason explains why the loop ended, so the log answers "why did it stop"
// without having to read the surrounding lines.
func stopReason(playCtx, ctx context.Context) string {
	switch {
	case ctx.Err() != nil:
		return "signal"
	case playCtx.Err() != nil:
		return "done"
	default:
		return "session ended"
	}
}
