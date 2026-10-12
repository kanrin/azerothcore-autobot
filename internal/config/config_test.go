package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envOf builds an envLookup from a map so tests never touch the process
// environment (and can therefore run in parallel without order dependence).
func envOf(kv map[string]string) envLookup {
	return func(key string) (string, bool) {
		v, ok := kv[key]
		return v, ok
	}
}

// writeProfile writes a profile into a temp dir and returns its path.
func writeProfile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "profile.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load("", envOf(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.AuthAddr != "127.0.0.1:3724" {
		t.Errorf("AuthAddr = %q, want the stock AC port", cfg.AuthAddr)
	}
	if cfg.Account.Prefix != "autobot" {
		t.Errorf("Account.Prefix = %q, want autobot", cfg.Account.Prefix)
	}
	if cfg.Character.Race != 1 || cfg.Character.Class != 1 {
		t.Errorf("default character = race %d class %d, want human warrior (1/1)",
			cfg.Character.Race, cfg.Character.Class)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("defaults must validate, got: %v", err)
	}
}

func TestLoadProfileAndEnvPrecedence(t *testing.T) {
	path := writeProfile(t, `
auth_addr: "10.0.0.1:3724"
realm_index: 2
account:
  prefix: "autobot"
  username: "autobot02"
  password_env: "PROFILE_PASSWORD"
character:
  name: "Autobottwo"
  race: 3
  class: 5
  gender: 1
database:
  auth_dsn_env: "PROFILE_AUTH_DSN"
behavior:
  accept_party_invites: true
  invite_allow_prefixes: ["autobot"]
  lfg:
    enabled: true
    roles: ["TANK"]
    dungeons: ["random"]
`)

	t.Run("profile values are used", func(t *testing.T) {
		cfg, err := load(path, envOf(nil))
		if err != nil {
			t.Fatalf("load: %v", err)
		}

		checks := []struct {
			name string
			got  any
			want any
		}{
			{"AuthAddr", cfg.AuthAddr, "10.0.0.1:3724"},
			{"RealmIndex", cfg.RealmIndex, 2},
			{"Account.Username", cfg.Account.Username, "autobot02"},
			{"Account.PasswordEnv", cfg.Account.PasswordEnv, "PROFILE_PASSWORD"},
			{"Character.Name", cfg.Character.Name, "Autobottwo"},
			{"Character.Race", cfg.Character.Race, uint8(3)},
			{"Character.Class", cfg.Character.Class, uint8(5)},
			{"Character.Gender", cfg.Character.Gender, uint8(1)},
			{"Database.AuthDSNEnv", cfg.Database.AuthDSNEnv, "PROFILE_AUTH_DSN"},
			{"Behavior.AcceptPartyInvites", cfg.Behavior.AcceptPartyInvites, true},
			{"Behavior.LFG.Enabled", cfg.Behavior.LFG.Enabled, true},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		}
		if cfg.Path != path {
			t.Errorf("Path = %q, want %q", cfg.Path, path)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("profile must validate, got: %v", err)
		}
	})

	t.Run("environment wins over the profile", func(t *testing.T) {
		cfg, err := load(path, envOf(map[string]string{
			"AUTOBOT_AUTH_ADDR":       "192.168.50.11:3724",
			"AUTOBOT_REALM_INDEX":     "0",
			"AUTOBOT_ACCOUNT":         "autobotenv",
			"AUTOBOT_CHARACTER_NAME":  "Autobotenv",
			"AUTOBOT_CHARACTER_RACE":  "1",
			"AUTOBOT_CHARACTER_CLASS": "1",
		}))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.AuthAddr != "192.168.50.11:3724" {
			t.Errorf("AuthAddr = %q, want the env value", cfg.AuthAddr)
		}
		if cfg.RealmIndex != 0 {
			t.Errorf("RealmIndex = %d, want the env value 0", cfg.RealmIndex)
		}
		if cfg.Account.Username != "autobotenv" || cfg.Character.Name != "Autobotenv" {
			t.Errorf("names = %q/%q, want the env values", cfg.Account.Username, cfg.Character.Name)
		}
		if cfg.Character.Race != 1 || cfg.Character.Class != 1 {
			t.Errorf("race/class = %d/%d, want the env values", cfg.Character.Race, cfg.Character.Class)
		}
	})

	t.Run("E2E names are the fallback for the address", func(t *testing.T) {
		cfg, err := load("", envOf(map[string]string{"E2E_AUTH_ADDR": "10.1.2.3:3724"}))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.AuthAddr != "10.1.2.3:3724" {
			t.Errorf("AuthAddr = %q, want the E2E_AUTH_ADDR fallback", cfg.AuthAddr)
		}
	})
}

func TestLoadRejectsUnknownKeysAndBadNumbers(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		env     map[string]string
	}{
		{
			name:    "misspelled profile key",
			profile: "auth_addrr: \"127.0.0.1:3724\"\n",
		},
		{
			name:    "misspelled nested key",
			profile: "character:\n  nmae: \"Autobot\"\n",
		},
		{
			name: "malformed realm index",
			env:  map[string]string{"AUTOBOT_REALM_INDEX": "one"},
		},
		{
			name: "malformed race",
			env:  map[string]string{"AUTOBOT_CHARACTER_RACE": "human"},
		},
		{
			name: "out of range class",
			env:  map[string]string{"AUTOBOT_CHARACTER_CLASS": "300"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := ""
			if tc.profile != "" {
				path = writeProfile(t, tc.profile)
			}
			if _, err := load(path, envOf(tc.env)); err == nil {
				t.Fatalf("load succeeded, want an error")
			}
		})
	}
}

func TestResolveSecrets(t *testing.T) {
	t.Run("profile-named variables win over the defaults", func(t *testing.T) {
		path := writeProfile(t, `
account:
  prefix: "autobot"
  username: "autobot02"
  password_env: "PROFILE_PASSWORD"
database:
  auth_dsn_env: "PROFILE_AUTH_DSN"
`)
		env := envOf(map[string]string{
			"PROFILE_PASSWORD": "s3cret",
			"PROFILE_AUTH_DSN": "profile:auth@tcp(h:3306)/acore_auth",
			"AUTOBOT_PASSWORD": "ignored",
			"AUTOBOT_AUTH_DSN": "ignored",
			"AUTOBOT_CHAR_DSN": "autobot:char@tcp(h:3306)/acore_characters",
			"E2E_WORLD_DSN":    "e2e:world@tcp(h:3306)/acore_world",
		})

		cfg, err := load(path, env)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := cfg.ResolveSecrets(env); err != nil {
			t.Fatalf("ResolveSecrets: %v", err)
		}

		checks := []struct {
			name string
			got  string
			want string
		}{
			{"Password", cfg.Account.Password, "s3cret"},
			{"AuthDSN", cfg.Database.AuthDSN, "profile:auth@tcp(h:3306)/acore_auth"},
			{"CharDSN", cfg.Database.CharDSN, "autobot:char@tcp(h:3306)/acore_characters"},
			{"WorldDSN", cfg.Database.WorldDSN, "e2e:world@tcp(h:3306)/acore_world"},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
			}
		}
	})

	t.Run("every missing variable is reported at once", func(t *testing.T) {
		path := writeProfile(t, `
account:
  prefix: "autobot"
  password_env: "PROFILE_PASSWORD"
`)
		env := envOf(map[string]string{"AUTOBOT_CHAR_DSN": "only-this-one"})

		cfg, err := load(path, env)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		err = cfg.ResolveSecrets(env)
		if !errors.Is(err, ErrMissingSecret) {
			t.Fatalf("ResolveSecrets error = %v, want ErrMissingSecret", err)
		}
		for _, want := range []string{"PROFILE_PASSWORD", "AUTOBOT_AUTH_DSN", "E2E_AUTH_DSN", "AUTOBOT_WORLD_DSN", "E2E_WORLD_DSN"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %s", err, want)
			}
		}
		if strings.Contains(err.Error(), "AUTOBOT_CHAR_DSN") {
			t.Errorf("error %q reports a variable that was set", err)
		}
	})
}

func TestResolveSecretsCanBeSplit(t *testing.T) {
	dsnOnly := envOf(map[string]string{
		"E2E_AUTH_DSN":  "auth-dsn",
		"E2E_CHAR_DSN":  "char-dsn",
		"E2E_WORLD_DSN": "world-dsn",
	})

	// The account commands list and delete accounts, which needs the databases
	// but no password; a missing secret they do not use must not block them.
	t.Run("DSNs resolve without a password", func(t *testing.T) {
		cfg := Default()
		if err := cfg.ResolveDatabaseSecrets(dsnOnly); err != nil {
			t.Fatalf("ResolveDatabaseSecrets: %v", err)
		}
		if cfg.Database.AuthDSN != "auth-dsn" || cfg.Database.CharDSN != "char-dsn" || cfg.Database.WorldDSN != "world-dsn" {
			t.Errorf("DSNs = %q / %q / %q, want the three values from the environment",
				cfg.Database.AuthDSN, cfg.Database.CharDSN, cfg.Database.WorldDSN)
		}
		if cfg.Account.Password != "" {
			t.Errorf("Password = %q, want it untouched by ResolveDatabaseSecrets", cfg.Account.Password)
		}
	})

	// Provisioning needs the password, and a DSN typo must not hide that.
	t.Run("the password resolves without DSNs", func(t *testing.T) {
		cfg := Default()
		if err := cfg.ResolvePasswordSecret(envOf(map[string]string{"AUTOBOT_PASSWORD": "pw"})); err != nil {
			t.Fatalf("ResolvePasswordSecret: %v", err)
		}
		if cfg.Account.Password != "pw" {
			t.Errorf("Password = %q, want %q", cfg.Account.Password, "pw")
		}
		if cfg.Database.AuthDSN != "" {
			t.Errorf("AuthDSN = %q, want it untouched by ResolvePasswordSecret", cfg.Database.AuthDSN)
		}
	})

	t.Run("a missing password is still an error", func(t *testing.T) {
		cfg := Default()
		err := cfg.ResolvePasswordSecret(dsnOnly)
		if !errors.Is(err, ErrMissingSecret) {
			t.Fatalf("ResolvePasswordSecret without a password in the environment = %v, want ErrMissingSecret", err)
		}
	})
}

// TestExportedGuards covers the two checks the fleet layer reuses: the prefix
// rule and the race/class matrix. They are exported precisely so there is one
// copy of each, which means they need to be tested through the exported door.
func TestExportedGuards(t *testing.T) {
	if err := ValidPrefix("autobot"); err != nil {
		t.Errorf("ValidPrefix(autobot) = %v, want nil", err)
	}
	if err := ValidPrefix("z"); err == nil {
		t.Error("ValidPrefix(z) = nil, want an error for a one-character namespace")
	}
	if err := ValidPrefix("au%"); err == nil {
		t.Error("ValidPrefix(au%) = nil, want an error for a wildcard")
	}

	if !ValidRaceClass(1, 1) {
		t.Error("ValidRaceClass(1, 1) = false, want true: human warrior exists")
	}
	if ValidRaceClass(1, 3) {
		t.Error("ValidRaceClass(1, 3) = true, want false: there is no human hunter in 3.3.5a")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr error
	}{
		{
			name:   "defaults are valid",
			mutate: func(*Config) {},
		},
		{
			name:    "auth address without a port",
			mutate:  func(c *Config) { c.AuthAddr = "127.0.0.1" },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "auth address with port zero",
			mutate:  func(c *Config) { c.AuthAddr = "127.0.0.1:0" },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "empty account prefix",
			mutate:  func(c *Config) { c.Account.Prefix = "" },
			wantErr: ErrInvalidProfile,
		},
		{
			// A one-letter prefix would claim every account starting with that
			// letter, so the guard would stop being a guard.
			name:    "account prefix that is too short",
			mutate:  func(c *Config) { c.Account.Prefix = "z" },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "account prefix containing a wildcard",
			mutate:  func(c *Config) { c.Account.Prefix = "au%" },
			wantErr: ErrInvalidProfile,
		}, {
			name: "custom prefix with a matching account name",
			mutate: func(c *Config) {
				c.Account.Prefix = "zhl"
				c.Account.Username = "zhl001"
				c.Character = Character{Name: "Zhlone", Race: 1, Class: 1}
			},
		},
		{
			name:    "negative realm index",
			mutate:  func(c *Config) { c.RealmIndex = -1 },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "account outside the bot prefix",
			mutate:  func(c *Config) { c.Account.Username = "realplayer" },
			wantErr: ErrBotPrefix,
		},
		{
			name: "character outside the bot prefix",
			mutate: func(c *Config) {
				c.Account.Username = "autobot01"
				c.Character.Name = "Thrall"
			},
			wantErr: ErrBotPrefix,
		},
		{
			name: "character name stored differently by the server",
			mutate: func(c *Config) {
				c.Account.Prefix = "autobot"
				c.Character.Name = "autobotone" // AC stores Autobotone
			},
			wantErr: ErrInvalidProfile,
		},
		{
			name: "character name with a digit",
			mutate: func(c *Config) {
				c.Character.Name = "Autobot1"
			},
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "race that is not playable",
			mutate:  func(c *Config) { c.Character.Race = 9 },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "class that does not exist",
			mutate:  func(c *Config) { c.Character.Class = 10 },
			wantErr: ErrInvalidProfile,
		},
		{
			// Observed live: the server refuses this with CHAR_CREATE_ERROR and
			// "invalid race/class pair (1/3)" in its log. Human hunters only
			// became possible in Cataclysm, not in 3.3.5a.
			name:    "race and class that 3.3.5a does not combine",
			mutate:  func(c *Config) { c.Character.Race, c.Character.Class = 1, 3 },
			wantErr: ErrInvalidProfile,
		},
		{
			name:    "gender out of range",
			mutate:  func(c *Config) { c.Character.Gender = 2 },
			wantErr: ErrInvalidProfile,
		},
		{
			name: "valid bot configuration",
			mutate: func(c *Config) {
				c.AuthAddr = "192.168.50.11:3724"
				c.Account.Username = "autobotprobe"
				c.Character = Character{Name: "Autobotprobe", Race: 1, Class: 1, Gender: 0}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)

			err := cfg.Validate()
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("Validate = %v, want nil", err)
			case tc.wantErr != nil && err == nil:
				t.Fatalf("Validate = nil, want %v", tc.wantErr)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("Validate = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMissingProfile(t *testing.T) {
	_, err := load(filepath.Join(t.TempDir(), "absent.yml"), envOf(nil))
	if err == nil {
		t.Fatal("load succeeded, want an error for a missing profile file")
	}
	if !strings.Contains(err.Error(), "read profile") {
		t.Errorf("error = %v, want it to mention reading the profile", err)
	}
}
