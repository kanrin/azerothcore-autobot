package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"azerothcore-autobot/internal/bot"
	"azerothcore-autobot/internal/wowdata"
)

// RunOptions tunes the supervisor.
type RunOptions struct {
	OpsOptions

	// For ends the run after this long. Zero means "until the context is done",
	// which is what Ctrl-C does.
	For time.Duration
}

// BotOutcome is what one bot did during a supervised run.
type BotOutcome struct {
	Name     string
	Logins   int
	Sessions int
	Ticks    int64
	Errors   []error
}

// RunOutcome is the summary of a supervised run.
type RunOutcome struct {
	Bots []BotOutcome

	// Started and Stopped bound the run, which is what makes "it stayed up for two
	// hours" a fact rather than an impression.
	Started time.Time
	Stopped time.Time
}

// Uptime is how long the fleet was up.
func (o RunOutcome) Uptime() time.Duration { return o.Stopped.Sub(o.Started) }

// Err joins every bot's errors.
func (o RunOutcome) Err() error {
	var errs []error
	for _, b := range o.Bots {
		errs = append(errs, b.Errors...)
	}
	return errors.Join(errs...)
}

// Clean is how many bots ended without a failure.
func (o RunOutcome) Clean() int {
	n := 0
	for _, b := range o.Bots {
		if len(b.Errors) == 0 {
			n++
		}
	}
	return n
}

// Supervise runs the whole roster until told to stop.
//
// Each bot logs in, plays and logs out on its own goroutine, and a bot whose
// session dies is logged in again — up to the policy's attempt count — because
// that is what "resident" means in practice: servers drop connections, and a
// fleet that gives up on the first one is not a fleet. The logins are paced so
// the authserver is never handed a burst.
//
// Stopping is graceful and concurrent: every bot is asked to stop, the loops are
// waited for, and then each logs out, so the 20 second logout countdown runs in
// parallel instead of once per bot.
func Supervise(ctx context.Context, f *Fleet, data *wowdata.Client, password string, opts RunOptions) (RunOutcome, error) {
	opts.OpsOptions = opts.OpsOptions.withDefaults()

	outcome := RunOutcome{Started: time.Now()}
	outcome.Bots = make([]BotOutcome, len(f.Bots))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if opts.For > 0 {
		var stopFor context.CancelFunc
		runCtx, stopFor = context.WithTimeout(runCtx, opts.For)
		defer stopFor()
	}

	// gate paces the logins: at most MaxConcurrent are in flight, and each start
	// is staggered, because the authserver reads the logon proof on one thread.
	gate := make(chan struct{}, max(1, f.Policy.Login.MaxConcurrent))

	var wg sync.WaitGroup
	for i := range f.Bots {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcome.Bots[i] = superviseBot(runCtx, f, f.Bots[i], data, password, gate, i, opts)
		}(i)
	}

	wg.Wait()
	outcome.Stopped = time.Now()

	opts.Logger.Info("fleet: all bots stopped", "bots", len(f.Bots),
		"uptime", outcome.Uptime().Round(time.Second).String(), "clean", outcome.Clean())
	return outcome, nil
}

// superviseBot keeps one bot online: login, play, log out, and try again while it
// still has attempts left. It returns what the bot did.
func superviseBot(ctx context.Context, f *Fleet, b Bot, data *wowdata.Client, password string,
	gate chan struct{}, index int, opts RunOptions) BotOutcome {

	outcome := BotOutcome{Name: b.Name}

	for attempt := 1; attempt <= f.Policy.Login.Attempts; attempt++ {
		if ctx.Err() != nil {
			return outcome
		}

		runner, err := bot.New(specFor(f, b, password, b.FeaturesFor(f.Policy)), bot.Options{
			Tick:         200 * time.Millisecond,
			Logf:         opts.Logf,
			Logger:       opts.Logger,
			LoginTimeout: opts.Timeout,
		})
		if err != nil {
			outcome.Errors = append(outcome.Errors, err)
			return outcome
		}

		if err := loginWithGate(ctx, runner, data, f, gate, index, opts); err != nil {
			outcome.Errors = append(outcome.Errors, fmt.Errorf("attempt %d: %w", attempt, err))
			opts.Logger.Warn("fleet: login failed", "bot", b.Name, "attempt", attempt,
				"of", f.Policy.Login.Attempts, "err", err.Error())
			if err := sleepCtx(ctx, backoffForAttempt(f, attempt)); err != nil {
				return outcome
			}
			continue
		}
		outcome.Logins++

		playErr := runner.Play(ctx)
		outcome.Sessions++
		outcome.Ticks += runner.Blackboard().Tick()

		// The logout runs with a context of its own: when the stop came from
		// outside, the run context is already cancelled and a graceful logout is
		// exactly what still has to happen.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.Timeout)
		if err := runner.Close(closeCtx); err != nil {
			outcome.Errors = append(outcome.Errors, fmt.Errorf("logout: %w", err))
		}
		cancel()

		if playErr != nil {
			outcome.Errors = append(outcome.Errors, playErr)
			opts.Logger.Warn("fleet: session ended", "bot", b.Name, "err", playErr.Error())
		}
		if ctx.Err() != nil {
			// A cancelled context is the operator stopping the fleet, not a reason
			// to reconnect.
			return outcome
		}
		if err := sleepCtx(ctx, backoffForAttempt(f, attempt)); err != nil {
			return outcome
		}
	}

	opts.Logger.Error("fleet: bot gave up", "bot", b.Name, "attempts", f.Policy.Login.Attempts)
	return outcome
}

// loginWithGate logs one bot in without handing the authserver a burst: the gate
// bounds how many logins are in flight and the stagger spaces the starts out.
func loginWithGate(ctx context.Context, runner *bot.Runner, data *wowdata.Client, f *Fleet,
	gate chan struct{}, index int, opts RunOptions) error {

	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-gate }()

	if index > 0 {
		if err := sleepCtx(ctx, f.Policy.Login.Stagger); err != nil {
			return err
		}
	}

	loginCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	return runner.Login(loginCtx, data)
}

// backoffForAttempt spaces out one bot's retries. The first one comes back
// quickly, in case the server was simply busy.
func backoffForAttempt(f *Fleet, attempt int) time.Duration {
	return time.Duration(attempt) * f.Policy.Login.Stagger
}

// discardLogger is the default logger for a caller that passes none.
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
