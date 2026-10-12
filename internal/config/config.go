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

// DefaultPassword is the password bot accounts get when nothing else provides one.
//
// It exists so a local stack works out of the box: every account this project
// creates only ever logs into the operator's own server, and the alternative —
// refusing to start until a variable is exported — makes the first run a puzzle.
//
// It is the last step of the precedence: --password, then the profile's
// account.password_env, then AUTOBOT_PASSWORD, and only then this. It is not a
// secret worth keeping — anyone who can reach the authserver of a server set up
// this way knows it — so it must never be relied on where strangers can reach the
// authserver.
const DefaultPassword = "botbot"

// ResolveSecrets fills the password and the three DSNs from the environment.
//
// Per value it tries the variable named by the profile first, then this
// project's AUTOBOT_* name, then upstream's E2E_* name. Every missing variable
// is reported in one error so a fresh checkout takes one fix, not three runs.
func (c *Config) ResolveSecrets(lookup envLookup) error {
	return errors.Join(c.ResolvePasswordSecret(lookup), c.ResolveDatabaseSecrets(lookup))
}

// ResolvePasswordSecret fills the account password from the environment, and
// falls back to DefaultPassword when nothing is set.
//
// It is separate from the DSNs because not every command needs a password:
// provisioning can take one from --password, and listing or deleting accounts
// needs none at all. Making "which accounts exist" depend on a secret would be
// an obstacle with no benefit.
func (c *Config) ResolvePasswordSecret(lookup envLookup) error {
	return resolveSecretOrDefault(lookup,
		secretNames(c.Account.PasswordEnv, "AUTOBOT_PASSWORD"), &c.Account.Password, DefaultPassword)
}

// ResolveDatabaseSecrets fills the three database DSNs from the environment.
func (c *Config) ResolveDatabaseSecrets(lookup envLookup) error {
	return errors.Join(
		resolveSecret(lookup, "auth database DSN",
			secretNames(c.Database.AuthDSNEnv, "AUTOBOT_AUTH_DSN", "E2E_AUTH_DSN"), &c.Database.AuthDSN),
		resolveSecret(lookup, "characters database DSN",
			secretNames(c.Database.CharDSNEnv, "AUTOBOT_CHAR_DSN", "E2E_CHAR_DSN"), &c.Database.CharDSN),
		resolveSecret(lookup, "world database DSN",
			secretNames(c.Database.WorldDSNEnv, "AUTOBOT_WORLD_DSN", "E2E_WORLD_DSN"), &c.Database.WorldDSN),
	)
}

// secretNames collects the candidate variables for one value: the one the
// profile named first, then the built-in fallbacks.
func secretNames(fromProfile string, defaults ...string) []string {
	out := make([]string, 0, 1+len(defaults))
	if fromProfile != "" {
		out = append(out, fromProfile)
	}
	return append(out, defaults...)
}

// resolveSecret reads the first candidate that is set, or reports every
// candidate name so that one run fixes the whole file.
func resolveSecret(lookup envLookup, what string, candidates []string, dst *string) error {
	if v, ok := lookupFirst(lookup, candidates); ok {
		*dst = v
		return nil
	}
	return fmt.Errorf("%w: set %s (the %s)", ErrMissingSecret, strings.Join(candidates, ", "), what)
}

// resolveSecretOrDefault reads the first candidate that is set and takes fallback
// when none is.
//
// It never fails, so it has no "what" in its signature, unlike resolveSecret. It is
// used for the bot account password, which has a documented default, and
// deliberately not for the DSNs: a default DSN would silently connect somewhere
// unexpected, while a default bot password only ever reaches this project's own
// accounts.
func resolveSecretOrDefault(lookup envLookup, candidates []string, dst *string, fallback string) error {
	if v, ok := lookupFirst(lookup, candidates); ok {
		*dst = v
		return nil
	}
	*dst = fallback
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
	} else if err := checkPrefixShape(c.Account.Prefix); err != nil {
		errs = append(errs, err)
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
	if err := checkRaceClass(c.Character.Race, c.Character.Class); err != nil {
		errs = append(errs, err)
	}
	if c.Character.Gender > 1 {
		errs = append(errs, fmt.Errorf("%w: character.gender %d must be 0 (male) or 1 (female)",
			ErrInvalidProfile, c.Character.Gender))
	}

	return errors.Join(errs...)
}

// ValidPrefix reports whether a bot prefix can serve as a namespace guard.
//
// It is exported because the fleet keeps its own prefix in fleet.yml and has to
// apply the same rule as a profile does — a second copy of this check is exactly
// how the two drift apart.
func ValidPrefix(prefix string) error { return checkPrefixShape(prefix) }

// ValidRaceClass reports whether AzerothCore can create this race/class pair.
//
// The fleet roster needs the same guard a profile gets: "human hunter" does not
// exist in 3.3.5a, and the server only says so at character creation, which is
// too late to be a pleasant way to find out.
func ValidRaceClass(race, class uint8) bool { return checkRaceClass(race, class) == nil }

// checkPrefixShape keeps the bot prefix usable as a namespace guard.
//
// The prefix decides which accounts and characters this project may touch, so a
// one-character or punctuated prefix is refused rather than accepted as a
// namespace: "z" would claim every account starting with z, and "%" would claim
// everything, which is exactly the situation the guard exists to prevent.
func checkPrefixShape(prefix string) error {
	if len(prefix) < 2 {
		return fmt.Errorf("%w: account.prefix %q is too short; use at least 2 characters",
			ErrInvalidProfile, prefix)
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return fmt.Errorf("%w: account.prefix %q contains %q; use letters and digits only",
				ErrInvalidProfile, prefix, r)
		}
	}
	return nil
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

// raceClasses lists the classes AzerothCore will create for each race. The
// range checks above cannot catch a pair the server rejects, which is not
// hypothetical: a human hunter passes both checks, and the server answers with
// CHAR_CREATE_ERROR plus
//
//	Player::Create: Possible hacking-attempt: ... invalid race/class pair (1/3)
//
// Human hunters only exist from Cataclysm on.
//
// Source: the server's own acore_world.playercreateinfo table, which is what
// Player::Create consults. Re-derive it with
//
//	docker exec ac-database mysql -uroot -ppassword -N -e \
//	  'SELECT race, GROUP_CONCAT(class ORDER BY class) FROM acore_world.playercreateinfo GROUP BY race'
//
// Class 6 (death knight) is listed for every race here, but a server additionally
// requires a level 55 character on the account; that rule stays server-side and
// the bot avoids death knights by default (see the project rules).
var raceClasses = map[uint8][]uint8{
	1:  {1, 2, 4, 5, 6, 8, 9}, // Human
	2:  {1, 3, 4, 6, 7, 9},    // Orc
	3:  {1, 2, 3, 4, 5, 6},    // Dwarf
	4:  {1, 3, 4, 5, 6, 11},   // Night elf
	5:  {1, 4, 5, 6, 8, 9},    // Undead
	6:  {1, 3, 6, 7, 11},      // Tauren
	7:  {1, 4, 6, 8, 9},       // Gnome
	8:  {1, 3, 4, 5, 6, 7, 8}, // Troll
	10: {2, 3, 4, 5, 6, 8, 9}, // Blood elf
	11: {1, 2, 3, 5, 6, 7, 8}, // Draenei
}

// checkRaceClass reports whether the server can create this combination.
//
// It stays quiet when either value is zero or unknown: the range checks already
// reported those, and reporting the same problem twice only makes the output
// noisier. The message names the classes that do work, because the useful thing
// to know when a combination is rejected is what to use instead.
func checkRaceClass(race, class uint8) error {
	classes, ok := raceClasses[race]
	if !ok || class == 0 {
		return nil
	}
	for _, allowed := range classes {
		if allowed == class {
			return nil
		}
	}
	return fmt.Errorf("%w: a race %d character cannot be class %d; AzerothCore creates %v for that race",
		ErrInvalidProfile, race, class, classes)
}

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
