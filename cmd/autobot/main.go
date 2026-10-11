// Command autobot is the CLI of the client-side WoW 3.3.5a autobot.
//
// Subcommands:
//
//	version  print build information
//	probe    authenticate, open the world socket and report where it stopped
//	login    provision the account, log the character in, verify in the database
//
// Configuration precedence is flag > environment > profile > defaults; see
// internal/config for the details. Secrets (account password, database DSNs) are
// only ever read from the environment, never from the profile.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"azerothcore-autobot/internal/config"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wowdata"
)

// version is the CLI's own version, kept in sync with the project README.
const version = "0.1.0"

func main() {
	logger := newLogger()

	var err error
	switch cmd := argAt(1); cmd {
	case "version":
		err = runVersion(os.Stdout)
	case "probe":
		err = runProbe(os.Args[2:], logger)
	case "login":
		err = runLogin(os.Args[2:], logger)
	case "", "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "autobot: unknown subcommand %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}

	if err != nil {
		// One line naming the stage that failed, on stderr; the exit code carries
		// the rest. Scripts check the code, humans read the line.
		logger.Error(err.Error())
		os.Exit(1)
	}
}

// newLogger writes structured lines to stderr so stdout stays free for output a
// caller may want to parse.
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// logf adapts AzerothGhost's printf-style logger onto slog. How much the client
// prints is decided by the client's own level (E2E_WORLD_LOG); this only decides
// where the lines land.
func logf(logger *slog.Logger) func(format string, args ...interface{}) {
	return func(format string, args ...interface{}) {
		logger.Info(fmt.Sprintf(format, args...))
	}
}

func runVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "autobot %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	return err
}

// baseFlags holds the settings every subcommand shares.
type baseFlags struct {
	profile    string
	authAddr   string
	realmIndex int
	account    string
	charName   string
	race       int
	class      int
	gender     int
	timeout    time.Duration
}

// newBaseFlags registers the shared flags on fs.
func newBaseFlags(fs *flag.FlagSet) *baseFlags {
	f := &baseFlags{}
	fs.StringVar(&f.profile, "profile", defaultProfile(),
		"profile YAML 路径（默认取 $AUTOBOT_PROFILE 或 ~/.config/autobot/profiles/local-ac.yml）")
	fs.StringVar(&f.authAddr, "auth-addr", "", "认证服务器 host:port，覆盖 profile 与环境变量")
	fs.IntVar(&f.realmIndex, "realm", -1, "realm 索引，覆盖 profile")
	fs.StringVar(&f.account, "account", "", "账号名（必须带 bot 前缀）")
	fs.StringVar(&f.charName, "char-name", "", "角色名")
	fs.IntVar(&f.race, "race", 0, "创建角色时的种族")
	fs.IntVar(&f.class, "class", 0, "创建角色时的职业")
	fs.IntVar(&f.gender, "gender", -1, "0=男 1=女")
	fs.DurationVar(&f.timeout, "timeout", 3*time.Minute, "整个流程的上限")
	return f
}

// apply lays the explicitly-set flags over the loaded configuration. This is the
// last step of the precedence chain, which is why the "unset" sentinels above
// exist: a zero value has to mean "not given", not "set to zero".
func (f *baseFlags) apply(cfg *config.Config) error {
	if f.authAddr != "" {
		cfg.AuthAddr = f.authAddr
	}
	if f.realmIndex >= 0 {
		cfg.RealmIndex = f.realmIndex
	}
	if f.account != "" {
		cfg.Account.Username = f.account
	}
	if f.charName != "" {
		cfg.Character.Name = f.charName
	}
	if f.race != 0 {
		if f.race < 0 || f.race > 255 {
			return fmt.Errorf("autobot: --race %d is out of range", f.race)
		}
		cfg.Character.Race = uint8(f.race)
	}
	if f.class != 0 {
		if f.class < 0 || f.class > 255 {
			return fmt.Errorf("autobot: --class %d is out of range", f.class)
		}
		cfg.Character.Class = uint8(f.class)
	}
	if f.gender >= 0 {
		if f.gender > 255 {
			return fmt.Errorf("autobot: --gender %d is out of range", f.gender)
		}
		cfg.Character.Gender = uint8(f.gender)
	}
	return nil
}

// loadConfig loads the profile, applies the flags, resolves the secrets and then
// validates the result. Everything a command needs to start is decided here, so
// a bad configuration fails before any socket is opened.
func loadConfig(f *baseFlags) (*config.Config, error) {
	cfg, err := config.Load(f.profile)
	if err != nil {
		return nil, err
	}
	if err := f.apply(cfg); err != nil {
		return nil, err
	}
	if err := cfg.ResolveSecrets(os.LookupEnv); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultProfile is the profile used when --profile is absent.
//
// No profile at all is a valid setup: defaults plus the environment then decide
// everything, which is also how the live tests run.
func defaultProfile() string {
	if v := os.Getenv("AUTOBOT_PROFILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".config", "autobot", "profiles", "local-ac.yml")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// connectOptions maps the configuration onto the protocol layer's options.
func connectOptions(cfg *config.Config, logger *slog.Logger) wow.ConnectOptions {
	return wow.ConnectOptions{
		AuthAddr:   cfg.AuthAddr,
		Username:   cfg.Account.Username,
		Password:   cfg.Account.Password,
		RealmIndex: cfg.RealmIndex,
		Logf:       logf(logger),
	}
}

// dataOptions maps the configuration onto the data layer's options.
func dataOptions(cfg *config.Config, logger *slog.Logger) wowdata.Options {
	return wowdata.Options{
		AuthDSN:  cfg.Database.AuthDSN,
		CharDSN:  cfg.Database.CharDSN,
		WorldDSN: cfg.Database.WorldDSN,
		Prefix:   cfg.Account.Prefix,
		Logf:     logf(logger),
	}
}

// requireAccountName fails early, naming the three ways to set the account.
func requireAccountName(cfg *config.Config) error {
	if cfg.Account.Username != "" {
		return nil
	}
	return fmt.Errorf("autobot: no account name; set account.username in the profile, " +
		"AUTOBOT_ACCOUNT in the environment, or pass --account")
}

// argAt returns os.Args[i] or "" when the command line is shorter.
func argAt(i int) string {
	if i < len(os.Args) {
		return os.Args[i]
	}
	return ""
}
