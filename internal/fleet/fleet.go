// Package fleet runs several bots as one roster: it reads the configuration,
// keeps the namespace guard, and gives the CLI the operations that make a small
// fleet manageable (provision, status, gc, run).
//
// The split from internal/bot is deliberate: bot knows how to be one bot, fleet
// knows how a set of them is described, paced and accounted for. Nothing here
// decides what a bot does — that is the behaviour registry.
package fleet

import (
	"fmt"
	"time"
)

// Policy is fleet.yml: the settings every bot in the roster shares.
type Policy struct {
	// Prefix is the bot namespace. Every account and character in the roster has
	// to start with it, and nothing outside it is ever touched. Required.
	Prefix string `yaml:"prefix"`

	AuthAddr   string `yaml:"auth_addr"`
	RealmIndex int    `yaml:"realm_index"`

	// DataDir holds maps/, mmaps/ and vmaps/ for the bots that move.
	DataDir string `yaml:"data_dir"`

	// PasswordEnv names the environment variable holding the accounts' password.
	// The password itself is never read from a file.
	PasswordEnv string `yaml:"password_env"`

	// RosterDir holds the per-bot files, relative to fleet.yml.
	RosterDir string `yaml:"roster_dir"`

	Login   LoginPolicy   `yaml:"login"`
	Balance BalancePolicy `yaml:"balance"`

	// Features are what a bot does unless its own entry overrides them.
	Features Features `yaml:"features"`
}

// LoginPolicy paces the logins.
//
// The authserver is single-threaded, so a burst of logons makes it time out while
// reading the logon proof. Upstream retries three times for the same reason.
type LoginPolicy struct {
	Stagger       time.Duration `yaml:"stagger"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	Attempts      int           `yaml:"attempts"`
}

// BalancePolicy says how evenly the factions have to be split.
type BalancePolicy struct {
	// Enforce turns the tolerance into a failure instead of a warning: a fleet
	// that is meant to look like a population should not silently be one-sided.
	Enforce bool `yaml:"enforce"`

	// Tolerance is the largest accepted deviation, where deviation is
	// |alliance-horde|/total. Three bots cannot be split evenly, so the default
	// accepts 2/1 (0.33) and rejects 3/0 (1.0).
	Tolerance float64 `yaml:"tolerance"`
}

// Features is what one bot does.
type Features struct {
	Travel bool `yaml:"travel"`
}

// Bot is one roster entry.
type Bot struct {
	// Name labels the bot in logs and telemetry. Required.
	Name string `yaml:"name"`

	// Account and Character default to the entry's position and the name in
	// AzerothCore's letter rules; Apply fills them in.
	Account   string `yaml:"account"`
	Character string `yaml:"character"`

	Race   uint8 `yaml:"race"`
	Class  uint8 `yaml:"class"`
	Gender uint8 `yaml:"gender"`

	// Features overrides the policy's features for this bot.
	Features *Features `yaml:"features"`
}

// FeaturesFor returns the features this bot runs with.
func (b Bot) FeaturesFor(p Policy) Features {
	if b.Features != nil {
		return *b.Features
	}
	return p.Features
}

// Faction is an AzerothCore TeamId.
//
// The values are AC's own (TeamId in SharedDefines.h: ALLIANCE = 469, HORDE = 67)
// rather than a private enum, so a faction can be handed to the server later
// without a translation table in between.
type Faction int

// The two factions.
const (
	Alliance Faction = 469
	Horde    Faction = 67
)

// String renders the faction for logs and tables.
func (f Faction) String() string {
	switch f {
	case Alliance:
		return "alliance"
	case Horde:
		return "horde"
	default:
		return fmt.Sprintf("faction(%d)", int(f))
	}
}

// raceFactions maps the playable races of 3.3.5a to their side. Death knights do
// not appear: the race decides the faction, the class does not.
var raceFactions = map[uint8]Faction{
	1:  Alliance, // human
	3:  Alliance, // dwarf
	4:  Alliance, // night elf
	7:  Alliance, // gnome
	11: Alliance, // draenei
	2:  Horde,    // orc
	5:  Horde,    // undead
	6:  Horde,    // tauren
	8:  Horde,    // troll
	10: Horde,    // blood elf
}

// FactionOf returns the faction of a playable race.
func FactionOf(race uint8) (Faction, error) {
	f, ok := raceFactions[race]
	if !ok {
		return 0, fmt.Errorf("fleet: race %d has no faction (playable races are 1-8, 10, 11)", race)
	}
	return f, nil
}

// Faction returns this bot's faction.
func (b Bot) Faction() (Faction, error) { return FactionOf(b.Race) }

// Fleet is a policy plus its roster.
type Fleet struct {
	Policy Policy
	Bots   []Bot

	// Path and RosterFiles record where the roster came from, which is the first
	// thing to check when a bot is not the one that was expected.
	Path        string
	RosterFiles []string
}

// Split is the faction split of a roster.
type Split struct {
	Alliance int
	Horde    int
}

// SplitOf counts the factions of a roster. A bot whose race has no faction is
// counted in neither column, which is why Total can be smaller than len(Bots).
func SplitOf(bots []Bot) Split {
	var s Split
	for _, b := range bots {
		switch f, _ := b.Faction(); f {
		case Alliance:
			s.Alliance++
		case Horde:
			s.Horde++
		}
	}
	return s
}

// Total is the number of bots that have a faction.
func (s Split) Total() int { return s.Alliance + s.Horde }

// Deviation is how far the split is from even: 0 is a perfect split, 1 is
// entirely one faction. It is defined on the difference rather than on a
// per-faction ratio so that it reads the same way whichever side is bigger.
func (s Split) Deviation() float64 {
	total := s.Total()
	if total == 0 {
		return 0
	}
	diff := s.Alliance - s.Horde
	if diff < 0 {
		diff = -diff
	}
	return float64(diff) / float64(total)
}

// Within reports whether the split satisfies a tolerance.
func (s Split) Within(tolerance float64) bool { return s.Deviation() <= tolerance }

// String renders the split for tables and logs.
func (s Split) String() string {
	return fmt.Sprintf("%d alliance / %d horde (deviation %.2f)", s.Alliance, s.Horde, s.Deviation())
}
