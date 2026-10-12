package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azerothcore-autobot/internal/config"
)

// writeFleet lays out a fleet.yml and its roster in a temp dir and returns the
// path to fleet.yml. Files are written verbatim, so a test can express a typo the
// way an operator would make one.
func writeFleet(t *testing.T, fleetYML string, roster map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.yml")
	if err := os.WriteFile(path, []byte(fleetYML), 0o600); err != nil {
		t.Fatalf("write fleet.yml: %v", err)
	}
	if len(roster) > 0 {
		rosterDir := filepath.Join(dir, DefaultRosterDir)
		if err := os.MkdirAll(rosterDir, 0o750); err != nil {
			t.Fatalf("create roster dir: %v", err)
		}
		for name, body := range roster {
			if err := os.WriteFile(filepath.Join(rosterDir, name), []byte(body), 0o600); err != nil {
				t.Fatalf("write roster %s: %v", name, err)
			}
		}
	}
	return path
}

const goodPolicy = `
prefix: autobot
auth_addr: 192.168.50.11:3724
roster_dir: roster
features:
  travel: false
`

// TestLoad_AppliesDefaults covers what a roster entry does not have to write: the
// account name from the prefix and the entry's position, and the character name
// from the label. Both have to be stable, because a bot that got a different
// account on the second run would be provisioned twice.
func TestLoad_AppliesDefaults(t *testing.T) {
	path := writeFleet(t, goodPolicy, map[string]string{
		"01-alliance.yml": `
bots:
  - name: alpha
    race: 1
    class: 1
  - name: bravo
    race: 4
    class: 3
`,
		"02-horde.yml": `
bots:
  - name: charlie
    race: 2
    class: 1
`,
	})

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Bots) != 3 {
		t.Fatalf("loaded %d bots, want 3", len(f.Bots))
	}
	if len(f.RosterFiles) != 2 {
		t.Errorf("roster files = %v, want both files", f.RosterFiles)
	}

	wantAccounts := []string{"autobot01", "autobot02", "autobot03"}
	// The character name carries the prefix and is truncated to AzerothCore's 12
	// character limit, so the third one loses the end of its label.
	wantCharacters := []string{"Autobotalpha", "Autobotbravo", "Autobotcharl"}
	for i, b := range f.Bots {
		if b.Account != wantAccounts[i] {
			t.Errorf("bot %d account = %q, want %q", i, b.Account, wantAccounts[i])
		}
		if b.Character != wantCharacters[i] {
			t.Errorf("bot %d character = %q, want %q", i, b.Character, wantCharacters[i])
		}
	}

	// The second file must be read after the first, or the numbering would depend
	// on the filesystem's ordering.
	if f.Bots[2].Name != "charlie" {
		t.Errorf("bot 3 = %q, want charlie (files are read in name order)", f.Bots[2].Name)
	}
}

// TestLoad_PolicyDefaults checks the pacing and tolerance defaults, which decide
// how a fleet logs in and whether a split is acceptable.
func TestLoad_PolicyDefaults(t *testing.T) {
	path := writeFleet(t, goodPolicy, map[string]string{
		"a.yml": "bots:\n  - name: alpha\n    race: 1\n    class: 1\n",
	})
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if f.Policy.Login.Stagger != DefaultStagger || f.Policy.Login.MaxConcurrent != DefaultMaxConcurrent {
		t.Errorf("login defaults = %+v, want stagger %s and one concurrent login",
			f.Policy.Login, DefaultStagger)
	}
	if f.Policy.Login.Attempts != DefaultAttempts {
		t.Errorf("login attempts = %d, want %d", f.Policy.Login.Attempts, DefaultAttempts)
	}
	if f.Policy.Balance.Tolerance != DefaultTolerance {
		t.Errorf("tolerance = %v, want %v", f.Policy.Balance.Tolerance, DefaultTolerance)
	}
	if f.Policy.PasswordEnv != DefaultPasswordEnv {
		t.Errorf("password env = %q, want %q", f.Policy.PasswordEnv, DefaultPasswordEnv)
	}
}

// TestLoad_DigitPrefix covers the prefix that doubles as an account naming scheme:
// "bot2" makes sense for accounts (autobot01 is normal) but a character name may
// not contain a digit, so the character carries the prefix's letters.
func TestLoad_DigitPrefix(t *testing.T) {
	path := writeFleet(t, `
prefix: bot2
auth_addr: 127.0.0.1:3724
roster_dir: roster
`, map[string]string{
		"a.yml": "bots:\n  - name: alpha\n    race: 1\n    class: 1\n",
	})

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := f.Bots[0].Account; got != "bot201" {
		t.Errorf("account = %q, want bot201 (the prefix plus the position)", got)
	}
	if got := f.Bots[0].Character; got != "Botalpha" {
		t.Errorf("character = %q, want Botalpha (the prefix's letters plus the label)", got)
	}
}

// TestLoad_MissingFile checks the two ways a fleet file can be absent.
func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Error("Load of a missing file succeeded, want an error")
	}
	if _, err := Load(""); err == nil {
		t.Error(`Load("") succeeded, want an error`)
	}
}

// TestLoad_RejectsMistakes covers the checks that make a roster trustworthy: an
// unknown key, a name outside the prefix, a duplicate, and a race/class pair the
// server cannot create. The last one matters most — it is invisible until
// character creation, which is the worst moment to learn about it.
func TestLoad_RejectsMistakes(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		roster    map[string]string
		wantErr   error
		wantInMsg string
	}{
		{
			name:      "unknown key in fleet.yml",
			policy:    goodPolicy + "prefixx: autobot\n",
			roster:    map[string]string{"a.yml": "bots:\n  - name: alpha\n    race: 1\n    class: 1\n"},
			wantInMsg: "prefixx",
		},
		{
			name:      "unknown key in a roster entry",
			policy:    goodPolicy,
			roster:    map[string]string{"a.yml": "bots:\n  - name: alpha\n    racee: 1\n    class: 1\n"},
			wantInMsg: "racee",
		},
		{
			name:    "account outside the prefix",
			policy:  goodPolicy,
			roster:  map[string]string{"a.yml": "bots:\n  - name: alpha\n    account: someoneelse01\n    race: 1\n    class: 1\n"},
			wantErr: ErrNotBotName,
		},
		{
			name:    "character outside the prefix",
			policy:  goodPolicy,
			roster:  map[string]string{"a.yml": "bots:\n  - name: alpha\n    character: Somename\n    race: 1\n    class: 1\n"},
			wantErr: ErrNotBotName,
		},
		{
			name:   "two bots share an account",
			policy: goodPolicy,
			roster: map[string]string{"a.yml": "bots:\n" +
				"  - name: alpha\n    account: autobot01\n    race: 1\n    class: 1\n" +
				"  - name: bravo\n    account: autobot01\n    race: 2\n    class: 1\n"},
			wantInMsg: "used twice",
		},
		{
			name:      "a race/class pair the server cannot create",
			policy:    goodPolicy,
			roster:    map[string]string{"a.yml": "bots:\n  - name: alpha\n    race: 1\n    class: 3\n"},
			wantInMsg: "cannot create race 1 class 3",
		},
		{
			name:      "a race with no faction",
			policy:    goodPolicy,
			roster:    map[string]string{"a.yml": "bots:\n  - name: alpha\n    race: 9\n    class: 1\n"},
			wantInMsg: "no faction",
		},
		{
			name:      "no prefix at all",
			policy:    "auth_addr: 127.0.0.1:3724\n",
			roster:    map[string]string{"a.yml": "bots:\n  - name: alpha\n    race: 1\n    class: 1\n"},
			wantErr:   ErrNoPrefix,
			wantInMsg: "prefix",
		},
		{
			name:      "no roster files",
			policy:    goodPolicy,
			wantErr:   ErrNoBots,
			wantInMsg: "roster",
		},
		{
			name:      "an empty roster file",
			policy:    goodPolicy,
			roster:    map[string]string{"a.yml": "bots: []\n"},
			wantInMsg: "lists no bots",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFleet(t, tc.policy, tc.roster)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.wantErr)
			}
			if tc.wantInMsg != "" && !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantInMsg)
			}
		})
	}
}

// envOf builds an environment lookup from a map, so tests never touch the
// process environment.
func envOf(kv map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := kv[key]
		return v, ok
	}
}

// TestApplyEnv covers the host override, which is what lets one fleet.yml be used
// from a laptop and from the machine next to the server. Both spellings have to be
// accepted, because the live tests export the E2E_* names.
func TestApplyEnv(t *testing.T) {
	newFleet := func() *Fleet {
		return &Fleet{
			Policy: Policy{Prefix: "autobot", AuthAddr: "127.0.0.1:3724", RealmIndex: 0},
			Bots:   []Bot{{Name: "alpha", Account: "autobot01", Character: "Autobotalpha", Race: 1, Class: 1}},
		}
	}

	t.Run("AUTOBOT_AUTH_ADDR wins", func(t *testing.T) {
		f := newFleet()
		env := map[string]string{"AUTOBOT_AUTH_ADDR": "10.0.0.5:3724", "E2E_AUTH_ADDR": "other:1"}
		if err := f.ApplyEnv(envOf(env)); err != nil {
			t.Fatalf("ApplyEnv: %v", err)
		}
		if f.Policy.AuthAddr != "10.0.0.5:3724" {
			t.Errorf("auth addr = %q, want the AUTOBOT_ variable", f.Policy.AuthAddr)
		}
	})

	t.Run("E2E_AUTH_ADDR is the fallback", func(t *testing.T) {
		f := newFleet()
		if err := f.ApplyEnv(envOf(map[string]string{"E2E_AUTH_ADDR": "192.168.50.11:3724"})); err != nil {
			t.Fatalf("ApplyEnv: %v", err)
		}
		if f.Policy.AuthAddr != "192.168.50.11:3724" {
			t.Errorf("auth addr = %q, want the E2E_ variable", f.Policy.AuthAddr)
		}
	})

	t.Run("data dir comes from the environment", func(t *testing.T) {
		f := newFleet()
		if err := f.ApplyEnv(envOf(map[string]string{"AUTOBOT_DATA_DIR": "/srv/ac-data"})); err != nil {
			t.Fatalf("ApplyEnv: %v", err)
		}
		if f.Policy.DataDir != "/srv/ac-data" {
			t.Errorf("data dir = %q", f.Policy.DataDir)
		}
	})

	t.Run("an override that breaks the policy is still refused", func(t *testing.T) {
		f := newFleet()
		// An empty override must not erase the file's value, and a roster that was
		// valid before the overlay has to stay valid after it.
		if err := f.ApplyEnv(envOf(map[string]string{"AUTOBOT_AUTH_ADDR": ""})); err != nil {
			t.Fatalf("ApplyEnv with an empty variable: %v", err)
		}
		if f.Policy.AuthAddr != "127.0.0.1:3724" {
			t.Errorf("auth addr = %q, want the file's value kept", f.Policy.AuthAddr)
		}
	})
}

// TestSplit covers the faction arithmetic the balance policy is judged by.
func TestSplit(t *testing.T) {
	bot := func(race uint8) Bot { return Bot{Name: "b", Race: race} }

	tests := []struct {
		name      string
		bots      []Bot
		alliance  int
		horde     int
		deviation float64
		within    bool // with the default tolerance
	}{
		{name: "empty", bots: nil, deviation: 0, within: true},
		{name: "two and one", bots: []Bot{bot(1), bot(4), bot(2)},
			alliance: 2, horde: 1, deviation: 1.0 / 3.0, within: true},
		{name: "one and one", bots: []Bot{bot(1), bot(2)},
			alliance: 1, horde: 1, deviation: 0, within: true},
		{name: "all one side", bots: []Bot{bot(1), bot(3), bot(7)},
			alliance: 3, deviation: 1, within: false},
		{name: "three and two", bots: []Bot{bot(1), bot(3), bot(4), bot(2), bot(6)},
			alliance: 3, horde: 2, deviation: 0.2, within: true},
		{name: "four and one", bots: []Bot{bot(1), bot(3), bot(4), bot(7), bot(2)},
			alliance: 4, horde: 1, deviation: 0.6, within: false},
		{name: "a race without a faction is counted in neither column", bots: []Bot{bot(1), bot(9)},
			alliance: 1, deviation: 1, within: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitOf(tc.bots)
			if got.Alliance != tc.alliance || got.Horde != tc.horde {
				t.Errorf("Split = %d/%d, want %d/%d", got.Alliance, got.Horde, tc.alliance, tc.horde)
			}
			if diff := got.Deviation() - tc.deviation; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("Deviation = %v, want %v", got.Deviation(), tc.deviation)
			}
			if got.Within(DefaultTolerance) != tc.within {
				t.Errorf("Within(%v) = %v, want %v", DefaultTolerance, got.Within(DefaultTolerance), tc.within)
			}
		})
	}
}

// TestFactionOf checks the race table: the faction a bot belongs to is what
// "balanced" is measured against, so an unknown race has to be reported rather
// than silently counted as one side.
func TestFactionOf(t *testing.T) {
	for _, race := range []uint8{1, 3, 4, 7, 11} {
		if f, err := FactionOf(race); err != nil || f != Alliance {
			t.Errorf("FactionOf(%d) = %v/%v, want alliance", race, f, err)
		}
	}
	for _, race := range []uint8{2, 5, 6, 8, 10} {
		if f, err := FactionOf(race); err != nil || f != Horde {
			t.Errorf("FactionOf(%d) = %v/%v, want horde", race, f, err)
		}
	}
	if _, err := FactionOf(9); err == nil {
		t.Error("FactionOf(9) = nil error, want an error: race 9 is not playable")
	}
	if got := Alliance.String(); got != "alliance" {
		t.Errorf("Alliance.String() = %q", got)
	}
	if got := Horde.String(); got != "horde" {
		t.Errorf("Horde.String() = %q", got)
	}
}

// TestFeaturesFor covers the one thing a roster entry may override: what its bot does.
func TestFeaturesFor(t *testing.T) {
	policy := Policy{Features: Features{Travel: false}}

	if got := (Bot{Name: "a"}).FeaturesFor(policy); got.Travel {
		t.Error("a bot without its own features must inherit the policy's")
	}
	if got := (Bot{Name: "a", Features: &Features{Travel: true}}).FeaturesFor(policy); !got.Travel {
		t.Error("a bot with its own features must not inherit the policy's")
	}
}

// TestPassword reads the variable the policy names and falls back to the project's
// default, which is what lets a local fleet run without exporting anything.
func TestPassword(t *testing.T) {
	env := map[string]string{"AUTOBOT_PASSWORD": "s3cret", "OTHER_PASSWORD": "other"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	f := &Fleet{Policy: Policy{PasswordEnv: "OTHER_PASSWORD"}}
	got, err := f.Password(lookup)
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if got != "other" {
		t.Errorf("Password = %q, want the variable the policy names", got)
	}

	// An unset variable is not an error: the default is documented, and a local
	// fleet that has to export a variable before it can provision is a puzzle.
	f.Policy.PasswordEnv = "MISSING"
	got, err = f.Password(lookup)
	if err != nil {
		t.Fatalf("Password with an unset variable: %v", err)
	}
	if got != config.DefaultPassword {
		t.Errorf("Password with an unset variable = %q, want the default %q", got, config.DefaultPassword)
	}

	// An empty policy value falls back to the project default variable name.
	f.Policy.PasswordEnv = ""
	if got, err := f.Password(lookup); err != nil || got != "s3cret" {
		t.Errorf("Password with no configured variable = %q/%v, want AUTOBOT_PASSWORD", got, err)
	}
}
