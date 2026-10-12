// Package bot assembles one bot's lifetime: connect and log in, run the agent
// loop, log out cleanly.
//
// It is the composition root of the gameplay stack and the only place where the
// layers meet: internal/wow (protocol), internal/feature/* (gameplay) and
// internal/agent (priority). The feature packages stay free of the agent and of
// each other — each one's adapter lives here (behaviors.go and its siblings) —
// which is what keeps a feature testable without a registry and a registry
// testable without a server.
//
// One Runner owns exactly one session and one agent. internal/fleet runs several
// of them; the CLI runs one.
package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"azerothcore-autobot/internal/agent"
	"azerothcore-autobot/internal/feature/travel"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/state"
	"azerothcore-autobot/internal/wowdata"

	"github.com/azerothcore/AzerothGhost/movement"
	"github.com/azerothcore/AzerothGhost/navigation"
)

// Spec is everything one bot needs to exist and play.
type Spec struct {
	// Name labels the bot in logs and telemetry. The character name is used
	// when this is empty.
	Name string

	AuthAddr   string
	RealmIndex int

	Account  string
	Password string

	// Character is logged in, and created on the first login when the account
	// does not have it yet.
	Character string
	Race      uint8
	Class     uint8
	Gender    uint8

	// DataDir holds maps/, mmaps/ and vmaps/. Empty means no pathfinding, so no
	// movement behavior is registered: such a bot can log in and read its state,
	// it just cannot go anywhere.
	DataDir string

	Features Features
}

// Features switches the gameplay behaviors on.
//
// Everything defaults to off: a profile says what it wants instead of inheriting
// whatever the newest milestone added.
type Features struct {
	// Travel registers the walker, i.e. "this bot can be sent somewhere".
	Travel bool
}

// Label is the name this bot is logged under.
func (s Spec) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Character
}

// Provisioner makes sure an account exists before the bot dials. *wowdata.Client
// satisfies it; nil means "the account is already there".
type Provisioner interface {
	EnsureAccount(ctx context.Context, username, password string) (wowdata.Account, error)
}

// Options tunes one runner. The zero value is usable.
type Options struct {
	// Tick is the agent's cadence. 200ms matches the cadence upstream's bot uses.
	Tick time.Duration

	// KeepAlive is how often an idle bot pings. AzerothCore counts a ping that
	// arrives less than 27 seconds after the previous one as over-speed
	// (WorldSocket.cpp:741-770) and kicks a client that does it more than
	// CONFIG_MAX_OVERSPEED_PINGS times, so this has to stay above 27s. 30s is
	// what upstream's own bot uses.
	KeepAlive time.Duration

	// Heartbeat is how often an idle bot sends CMSG_HEARTBEAT. While the bot is
	// moving, the motion engine sends its own movement heartbeats instead.
	Heartbeat time.Duration

	AuthTimeout   time.Duration
	LoginTimeout  time.Duration
	LogoutTimeout time.Duration

	// Travel tunes the walker (arrival radius, stuck detection, replan budget).
	Travel travel.Options

	// Logf receives the AzerothGhost client's own lines.
	Logf func(format string, args ...interface{})

	// Logger receives this package's lines. Nil discards them.
	Logger *slog.Logger
}

// withDefaults fills in the zero fields.
func (o Options) withDefaults() Options {
	if o.Tick <= 0 {
		o.Tick = 200 * time.Millisecond
	}
	if o.KeepAlive <= 0 {
		o.KeepAlive = 30 * time.Second
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 5 * time.Second
	}
	if o.AuthTimeout <= 0 {
		o.AuthTimeout = 20 * time.Second
	}
	if o.LoginTimeout <= 0 {
		o.LoginTimeout = 90 * time.Second
	}
	if o.LogoutTimeout <= 0 {
		o.LogoutTimeout = 40 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return o
}

// Runner is one bot: a session, an agent and the behaviors between them.
type Runner struct {
	spec Spec
	opts Options

	sess   *wow.Session
	agent  *agent.Agent
	walker walker // non-nil when travel is enabled
	char   wow.Character

	// Keep-alive bookkeeping. Only the agent's goroutine touches these, which is
	// why they need no lock.
	lastPing      time.Time
	lastHeartbeat time.Time
	pingSeq       uint32
}

// New validates a spec and returns a runner that has not touched the network.
//
// The spec is checked here rather than on the first tick, because a bot with a
// missing account name or an impossible race/class pair should fail while the
// operator is still looking at the command line.
func New(spec Spec, opts Options) (*Runner, error) {
	switch {
	case spec.AuthAddr == "":
		return nil, errors.New("bot: auth address is required")
	case spec.Account == "":
		return nil, errors.New("bot: account is required")
	case spec.Password == "":
		return nil, errors.New("bot: password is required")
	case spec.Character == "":
		return nil, errors.New("bot: character name is required")
	}
	return &Runner{spec: spec, opts: opts.withDefaults()}, nil
}

// Spec returns the runner's spec.
func (r *Runner) Spec() Spec { return r.spec }

// Session returns the protocol session, or nil before Login.
func (r *Runner) Session() *wow.Session { return r.sess }

// Agent returns the agent, or nil before Login.
func (r *Runner) Agent() *agent.Agent { return r.agent }

// Character returns the logged-in character, or a zero value before Login.
func (r *Runner) Character() wow.Character { return r.char }

// Blackboard returns the agent's blackboard, or nil before Login.
func (r *Runner) Blackboard() *agent.Blackboard {
	if r.agent == nil {
		return nil
	}
	return r.agent.Blackboard()
}

// Login dials, makes sure the account exists, enters the world and builds the
// agent. prov may be nil, which means "the account is already provisioned".
//
// The character is created on the first login when the account does not have it:
// creating is a protocol operation, so it happens here rather than in the data
// layer.
func (r *Runner) Login(ctx context.Context, prov Provisioner) error {
	label := r.spec.Label()

	if prov != nil {
		acct, err := prov.EnsureAccount(ctx, r.spec.Account, r.spec.Password)
		if err != nil {
			return fmt.Errorf("bot %s: provision account: %w", label, err)
		}
		r.opts.Logger.Info("bot: account ready", "bot", label,
			"username", acct.Username, "id", acct.ID, "created", acct.Created)
	}

	sess, err := wow.Dial(ctx, wow.ConnectOptions{
		AuthAddr:    r.spec.AuthAddr,
		Username:    r.spec.Account,
		Password:    r.spec.Password,
		RealmIndex:  r.spec.RealmIndex,
		AuthTimeout: r.opts.AuthTimeout,
		Logf:        r.opts.Logf,
	})
	if err != nil {
		return fmt.Errorf("bot %s: %w", label, err)
	}
	r.sess = sess

	// The chat language of anything the bot says depends on the race, so it is
	// set before the world is entered.
	sess.SetCharRace(r.spec.Race)

	loginCtx, cancel := context.WithTimeout(ctx, r.opts.LoginTimeout)
	defer cancel()

	char, err := sess.EnterWorld(loginCtx, wow.EnterWorldOptions{
		Name:       r.spec.Character,
		Race:       r.spec.Race,
		Class:      r.spec.Class,
		Appearance: wow.DefaultAppearance(r.spec.Gender),
		Logf:       r.opts.Logf,
	})
	if err != nil {
		r.abandonSession()
		return fmt.Errorf("bot %s: enter world: %w", label, err)
	}
	r.char = char

	if err := r.buildGameplay(sess); err != nil {
		r.abandonSession()
		return fmt.Errorf("bot %s: %w", label, err)
	}

	reg, err := r.registry()
	if err != nil {
		r.abandonSession()
		return fmt.Errorf("bot %s: %w", label, err)
	}

	a, err := agent.New(label, reg, agent.Options{
		Tick:    r.opts.Tick,
		Logger:  r.opts.Logger,
		Refresh: r.refresh,
	})
	if err != nil {
		r.abandonSession()
		return fmt.Errorf("bot %s: %w", label, err)
	}
	r.agent = a

	// Publish the first self state immediately: the caller may want to send the bot
	// somewhere before the first tick, and a tick would otherwise be the first time
	// anything is on the blackboard.
	r.refresh(context.Background(), a.Blackboard())

	x, y, z, o, mapID := sess.Position()
	r.opts.Logger.Info("bot: in world", "bot", label, "character", char.Name, "guid", char.GUID,
		"level", char.Level, "map", mapID, "x", x, "y", y, "z", z, "orientation", o,
		"health", sess.Health(), "max_health", sess.MaxHealth(), "behaviors", len(reg.Behaviors()))
	return nil
}

// buildGameplay constructs whatever the enabled features need, once the session
// is in the world (the motion engine wants the server's move speed).
func (r *Runner) buildGameplay(sess *wow.Session) error {
	if !r.spec.Features.Travel {
		return nil
	}
	if r.spec.DataDir == "" {
		return errors.New("feature travel: no data_dir configured (needs maps/, mmaps/ and vmaps/)")
	}

	nav := navigation.NewEmbeddedNavigator(r.spec.DataDir)
	speed := sess.Client().MoveSpeed()
	if speed <= 0 {
		speed = defaultMoveSpeed
	}
	mover := movement.NewMovementController(movement.NewWorldMovementSender(sess.Client()),
		speed, nav, movement.DefaultMovementConfig())

	w, err := travel.New(nav, mover, r.opts.Travel)
	if err != nil {
		return fmt.Errorf("feature travel: %w", err)
	}
	r.walker = w
	r.opts.Logger.Info("bot: pathfinding ready", "bot", r.spec.Label(),
		"data_dir", r.spec.DataDir, "move_speed", speed)
	return nil
}

// defaultMoveSpeed is the run speed AzerothCore gives a player character
// (speed_run 7/7 in the client's movement tables). It is only the fallback for
// the window before the server's own speed is known.
const defaultMoveSpeed float32 = 7

// Play runs the agent until the context is cancelled or the session ends.
//
// Cancelling the caller's context is the normal way to stop a bot and returns
// nil; a session that ends on its own is an error, because from the operator's
// point of view the bot stopped working.
func (r *Runner) Play(ctx context.Context) error {
	if r.agent == nil || r.sess == nil {
		return errors.New("bot: Play called before Login")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.agent.Run(runCtx) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Run returns promptly on a cancelled context, and waiting for it here is
		// what guarantees that no behavior is still touching the session when the
		// caller goes on to close it.
		cancel()
		<-done
		return nil
	case <-r.sess.Done():
		cancel()
		<-done
		return fmt.Errorf("bot %s: session ended: %w", r.spec.Label(), r.sessionEndReason())
	}
}

// sessionEndReason explains why the read loop stopped, so that the log reads
// "connection reset" instead of a bare "session ended".
func (r *Runner) sessionEndReason() error {
	if err := r.sess.RunErr(); err != nil {
		return err
	}
	return errors.New("client read loop stopped")
}

// Close stops the agent, logs the character out and tears the socket down.
//
// The order is the project's shutdown rule: stop the loop, release the running
// behavior, then log out. A failed logout is returned but never prevents the
// socket from being closed — a socket left open keeps authenticating with the
// previous session key and breaks the next login of the same account.
func (r *Runner) Close(ctx context.Context) error {
	if r.agent != nil {
		r.agent.Stop(ctx)
	}
	if r.sess == nil {
		return nil
	}

	sess := r.sess
	r.sess = nil

	var err error
	select {
	case <-sess.Done():
		// The read loop is gone: there is nobody left to log out to.
	default:
		logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.LogoutTimeout)
		defer cancel()
		if err = sess.Logout(logoutCtx, r.opts.LogoutTimeout); err != nil {
			r.opts.Logger.Warn("bot: logout failed", "bot", r.spec.Label(), "err", err.Error())
		} else {
			r.opts.Logger.Info("bot: logged out", "bot", r.spec.Label(), "character", r.char.Name)
		}
	}
	sess.Close()
	return err
}

// abandonSession drops a session that failed before the bot started playing.
func (r *Runner) abandonSession() {
	if r.sess != nil {
		r.sess.Close()
		r.sess = nil
	}
}

// refresh runs at the top of every agent tick: it publishes what the behaviors
// read and keeps an idle connection alive.
func (r *Runner) refresh(_ context.Context, bb *agent.Blackboard) {
	if r.sess == nil {
		return
	}

	x, y, z, o, mapID := r.sess.Position()
	inWorld := r.sess.InWorld()

	bb.Set(SelfKey, Self{
		GUID:       r.char.GUID,
		MapID:      mapID,
		X:          x,
		Y:          y,
		Z:          z,
		O:          o,
		Health:     r.sess.Health(),
		MaxHealth:  r.sess.MaxHealth(),
		InCombat:   r.sess.InCombat(),
		TargetGUID: r.sess.TargetGUID(),
		InWorld:    inWorld,
	})

	// Money, bags and skills come from the client's cached update fields, so this
	// costs no packet. During a teleport that cache is being rebuilt, and a
	// half-built snapshot would read as "the bags are empty".
	if inWorld {
		bb.SetSnapshot(state.Read(r.sess))
		r.keepAlive(bb.Now())
	}
}

// keepAlive pings and heartbeats a bot that is standing still. A moving bot is
// kept alive by the motion engine's own movement heartbeats.
func (r *Runner) keepAlive(now time.Time) {
	if r.lastPing.IsZero() {
		// Start the clock at login: the server measures the gap between pings, and
		// one immediately after the handshake would count as an over-speed ping.
		r.lastPing, r.lastHeartbeat = now, now
		return
	}

	if now.Sub(r.lastHeartbeat) >= r.opts.Heartbeat {
		r.lastHeartbeat = now
		if err := r.sess.Client().SendHeartbeat(); err != nil {
			r.opts.Logger.Warn("bot: heartbeat failed", "bot", r.spec.Label(), "err", err.Error())
		}
	}
	if now.Sub(r.lastPing) >= r.opts.KeepAlive {
		r.lastPing = now
		r.pingSeq++
		if err := r.sess.Client().SendPing(r.pingSeq); err != nil {
			r.opts.Logger.Warn("bot: ping failed", "bot", r.spec.Label(), "err", err.Error())
		}
	}
}
