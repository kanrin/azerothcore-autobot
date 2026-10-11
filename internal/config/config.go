// Package config loads the bot profile and resolves everything that must not be
// stored in it.
//
// Precedence, highest first:
//
//	command-line flag  (applied by cmd/autobot on the loaded Config)
//	environment        (applied by Load)
//	profile YAML
//	built-in default
//
// Secrets never live in the profile: the password and the three database DSNs
// are named by the profile (password_env, auth_dsn_env, …) and read from the
// environment, so a committed profile is safe to share. Load resolves them via
// ResolveSecrets.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"azerothcore-autobot/internal/wow"

	"gopkg.in/yaml.v3"
)

// Configuration errors. All of them mean "the operator has to fix something
// before the bot can start", which the project's failure taxonomy calls an
// environment/precondition error: fail loudly at startup, never retry.
var (
	// ErrMissingSecret means an environment variable that holds a secret is not
	// set. The message lists every missing name, so one run fixes all of them.
	ErrMissingSecret = errors.New("config: missing secret")

	// ErrInvalidProfile means the profile is structurally unusable (bad address,
	// name that AzerothCore would reject, unknown race, …).
	ErrInvalidProfile = errors.New("config: invalid profile")

	// ErrBotPrefix means a name in the profile does not carry the bot prefix.
	// This is the guard that keeps the project from ever touching a real
	// player's account or character.
	ErrBotPrefix = errors.New("config: name outside the bot prefix")
)

// envLookup is os.LookupEnv's shape, injected so tests do not have to mutate
// the process environment.
type envLookup func(key string) (string, bool)

// Config is the resolved runtime configuration of one bot.
type Config struct {
	// AuthAddr is the authserver address, host:port.
	AuthAddr string `yaml:"auth_addr"`

	// RealmIndex selects an entry from the realm list the authserver returns.
	RealmIndex int `yaml:"realm_index"`

	// DataDir holds maps/, mmaps/ and vmaps/. Unused until the bot has to move
	// (milestone 2 onward), hence optional here.
	DataDir string `yaml:"data_dir"`

	Account   Account   `yaml:"account"`
	Character Character `yaml:"character"`
	Database  Database  `yaml:"database"`

	// Behavior carries the feature switches. It is parsed from the first day so
	// that profiles do not have to change when milestones 3 and 4 start reading
	// it; nothing consumes it yet.
	Behavior Behavior `yaml:"behavior"`

	// Path is the profile file this Config came from, or "" when it was built
	// purely from defaults and the environment.
	Path string `yaml:"-"`
}

// Account identifies the bot's login and the prefix that marks its data.
type Account struct {
	// Prefix marks every account and character this project owns. Provisioning
	// and cleanup refuse to touch anything that does not start with it.
	Prefix string `yaml:"prefix"`

	// Username is the login name; it must start with Prefix.
	Username string `yaml:"username"`

	// PasswordEnv names the environment variable holding the password. The
	// password itself is never read from the profile.
	PasswordEnv string `yaml:"password_env"`

	// Password is resolved from the environment by ResolveSecrets.
	Password string `yaml:"-"`
}

// Character selects or creates the character to play.
type Character struct {
	Name   string `yaml:"name"`
	Race   uint8  `yaml:"race"`
	Class  uint8  `yaml:"class"`
	Gender uint8  `yaml:"gender"`
}

// Database names the environment variables holding the DSNs, one per AC
// database. The DSNs themselves are resolved by ResolveSecrets.
type Database struct {
	AuthDSNEnv  string `yaml:"auth_dsn_env"`
	CharDSNEnv  string `yaml:"char_dsn_env"`
	WorldDSNEnv string `yaml:"world_dsn_env"`

	AuthDSN  string `yaml:"-"`
	CharDSN  string `yaml:"-"`
	WorldDSN string `yaml:"-"`
}

// Behavior is the policy block of the profile.
type Behavior struct {
	AcceptPartyInvites  bool     `yaml:"accept_party_invites"`
	InviteAllowPrefixes []string `yaml:"invite_allow_prefixes"`
	LFG                 LFG      `yaml:"lfg"`
}

// LFG is the dungeon-finder policy of the profile.
type LFG struct {
	Enabled  bool     `yaml:"enabled"`
	Roles    []string `yaml:"roles"`
	Dungeons []string `yaml:"dungeons"`
}

// Default returns the configuration used when a profile omits everything: the
// stock AC ports and the human warrior upstream's own harness creates.
func Default() *Config {
	return &Config{
		AuthAddr:   "127.0.0.1:3724",
		RealmIndex: 0,
		Account:    Account{Prefix: "autobot"},
		Character:  Character{Race: 1, Class: 1, Gender: 0},
	}
}

// Load reads a profile and applies the environment on top of it. An empty path
// means defaults plus environment only.
//
// Unknown YAML keys are an error rather than silently ignored: a misspelled
// key that quietly keeps its default is the kind of bug that only shows up on
// the live server.
func Load(path string) (*Config, error) { return load(path, os.LookupEnv) }

func load(path string, lookup envLookup) (*Config, error) {
	cfg := Default()
	cfg.Path = path

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read profile %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("config: parse profile %s: %w", path, err)
		}
	}

	if err := cfg.applyEnv(lookup); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnv overlays the environment on the profile.
//
// AUTOBOT_* is this project's namespace; the E2E_* names come from upstream's
// harness and are accepted as a fallback so that one .env file drives both the
// CLI and the live tests.
func (c *Config) applyEnv(lookup envLookup) error {
	c.AuthAddr = envString(lookup, c.AuthAddr, "AUTOBOT_AUTH_ADDR", "E2E_AUTH_ADDR")
	c.DataDir = envString(lookup, c.DataDir, "AUTOBOT_DATA_DIR")
	c.Account.Prefix = envString(lookup, c.Account.Prefix, "AUTOBOT_ACCOUNT_PREFIX")
	c.Account.Username = envString(lookup, c.Account.Username, "AUTOBOT_ACCOUNT")
	c.Character.Name = envString(lookup, c.Character.Name, "AUTOBOT_CHARACTER_NAME")

	var err error
	if c.RealmIndex, err = envInt(lookup, c.RealmIndex, "AUTOBOT_REALM_INDEX"); err != nil {
		return err
	}
	if c.Character.Race, err = envUint8(lookup, c.Character.Race, "AUTOBOT_CHARACTER_RACE"); err != nil {
		return err
	}
	if c.Character.Class, err = envUint8(lookup, c.Character.Class, "AUTOBOT_CHARACTER_CLASS"); err != nil {
		return err
	}
	c.Character.Gender, err = envUint8(lookup, c.Character.Gender, "AUTOBOT_CHARACTER_GENDER")
	return err
}

// ResolveSecrets fills the password and the three DSNs from the environment.
//
// Per value it tries the variable named by the profile first, then this
// project's AUTOBOT_* name, then upstream's E2E_* name. Every missing variable
// is reported in one error so a fresh checkout takes one fix, not three runs.
func (c *Config) ResolveSecrets(lookup envLookup) error {
	// names collects the candidates for one value, profile-named variable first.
	names := func(fromProfile string, defaults ...string) []string {
		out := make([]string, 0, 1+len(defaults))
		if fromProfile != "" {
			out = append(out, fromProfile)
		}
		return append(out, defaults...)
	}

	var missing []string
	resolve := func(candidates []string, dst *string) {
		if v, ok := lookupFirst(lookup, candidates); ok {
			*dst = v
			return
		}
		missing = append(missing, candidates...)
	}

	resolve(names(c.Account.PasswordEnv, "AUTOBOT_PASSWORD"), &c.Account.Password)
	resolve(names(c.Database.AuthDSNEnv, "AUTOBOT_AUTH_DSN", "E2E_AUTH_DSN"), &c.Database.AuthDSN)
	resolve(names(c.Database.CharDSNEnv, "AUTOBOT_CHAR_DSN", "E2E_CHAR_DSN"), &c.Database.CharDSN)
	resolve(names(c.Database.WorldDSNEnv, "AUTOBOT_WORLD_DSN", "E2E_WORLD_DSN"), &c.Database.WorldDSN)

	if len(missing) > 0 {
		return fmt.Errorf("%w: set %s", ErrMissingSecret, strings.Join(missing, ", "))
	}
	return nil
}

// lookupFirst returns the first of names that is set to a non-empty value.
func lookupFirst(lookup envLookup, names []string) (string, bool) {
	for _, n := range names {
		if v, ok := lookup(n); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// Validate checks the profile against the rules AzerothCore itself enforces, so
// that a bad value fails here with a readable message instead of surfacing much
// later as a CHAR_NAME_* code or a silently ignored packet.
//
// It returns ErrInvalidProfile (or ErrBotPrefix) joined with every problem it
// found, so one run reports all of them.
func (c *Config) Validate() error {
	var errs []error

	if _, port, err := net.SplitHostPort(c.AuthAddr); err != nil {
		errs = append(errs, fmt.Errorf("%w: auth_addr %q is not host:port", ErrInvalidProfile, c.AuthAddr))
	} else if p, convErr := strconv.Atoi(port); convErr != nil || p <= 0 || p > 65535 {
		errs = append(errs, fmt.Errorf("%w: auth_addr port %q is not a valid port", ErrInvalidProfile, port))
	}

	if c.RealmIndex < 0 {
		errs = append(errs, fmt.Errorf("%w: realm_index %d must not be negative", ErrInvalidProfile, c.RealmIndex))
	}

	if c.Account.Prefix == "" {
		errs = append(errs, fmt.Errorf(
			"%w: account.prefix is empty; the prefix is what keeps the bot out of real player data", ErrInvalidProfile))
	}
	if c.Account.Username != "" {
		errs = append(errs, c.checkBotName(c.Account.Username))
	}
	if c.Character.Name != "" {
		errs = append(errs, c.checkBotName(c.Character.Name), checkCharName(c.Character.Name))
	}

	if !validRaces[c.Character.Race] {
		errs = append(errs, fmt.Errorf("%w: character.race %d is not a playable 3.3.5a race",
			ErrInvalidProfile, c.Character.Race))
	}
	if !validClasses[c.Character.Class] {
		errs = append(errs, fmt.Errorf("%w: character.class %d is not a 3.3.5a class",
			ErrInvalidProfile, c.Character.Class))
	}
	if c.Character.Gender > 1 {
		errs = append(errs, fmt.Errorf("%w: character.gender %d must be 0 (male) or 1 (female)",
			ErrInvalidProfile, c.Character.Gender))
	}

	return errors.Join(errs...)
}

// checkBotName enforces the prefix rule on one name.
func (c *Config) checkBotName(name string) error {
	if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(c.Account.Prefix)) {
		return fmt.Errorf("%w: %q does not start with prefix %q", ErrBotPrefix, name, c.Account.Prefix)
	}
	return nil
}

// checkCharName reuses the protocol layer's sanitiser: a name that SanitizeName
// would rewrite is one AzerothCore rejects or stores differently, and a stored
// difference would break matching the character against the char list.
//
// Death knights (class 6) are deliberately not the default here: creating one
// needs a level 55 character on the account first (see the project rules).
func checkCharName(name string) error {
	if s := wow.SanitizeName(name); s != name {
		return fmt.Errorf("%w: character.name %q is not accepted by AzerothCore (it would become %q)",
			ErrInvalidProfile, name, s)
	}
	return nil
}

// Playable 3.3.5a races and classes.
//
// Source: azerothcore-wotlk/src/server/game/Miscellaneous/SharedDefines.h
// Races 1..11 and classes 1..11, each with one unused value (9 and 10). Whether
// a given race may play a given class is left to the server, which answers with
// a CHAR_CREATE_* code that Session.EnterWorld already reports.
var (
	validRaces = map[uint8]bool{
		1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 10: true, 11: true,
	}
	validClasses = map[uint8]bool{
		1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 11: true,
	}
)

// envString returns the first environment value that is set and non-empty, or
// the fallback when none is.
func envString(lookup envLookup, fallback string, names ...string) string {
	if v, ok := lookupFirst(lookup, names); ok {
		return v
	}
	return fallback
}

// envInt reads an integer environment variable. A malformed value is an error
// rather than a silent fallback: a typo that quietly keeps the default is far
// more expensive to find than a startup failure.
func envInt(lookup envLookup, fallback int, names ...string) (int, error) {
	for _, n := range names {
		v, ok := lookup(n)
		if !ok || v == "" {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fallback, fmt.Errorf("%w: %s=%q is not an integer", ErrInvalidProfile, n, v)
		}
		return parsed, nil
	}
	return fallback, nil
}

// envUint8 reads an 8-bit unsigned environment variable, with the same
// no-silent-fallback rule as envInt.
func envUint8(lookup envLookup, fallback uint8, names ...string) (uint8, error) {
	for _, n := range names {
		v, ok := lookup(n)
		if !ok || v == "" {
			continue
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(v), 10, 8)
		if err != nil {
			return fallback, fmt.Errorf("%w: %s=%q is not a number between 0 and 255", ErrInvalidProfile, n, v)
		}
		return uint8(parsed), nil
	}
	return fallback, nil
}
