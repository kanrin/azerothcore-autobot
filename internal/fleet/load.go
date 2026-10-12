package fleet

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"azerothcore-autobot/internal/config"
	"azerothcore-autobot/internal/wow"

	"gopkg.in/yaml.v3"
)

// Loading errors.
var (
	// ErrNoPrefix means fleet.yml has no prefix. Without one the namespace guard
	// cannot work, so loading is refused outright.
	ErrNoPrefix = errors.New("fleet: prefix is required")

	// ErrNotBotName means a roster entry names an account or character outside
	// the fleet's prefix.
	ErrNotBotName = errors.New("fleet: name outside the bot prefix")

	// ErrNoBots means fleet.yml points at a roster with no entries.
	ErrNoBots = errors.New("fleet: the roster is empty")
)

// Defaults, applied to whatever fleet.yml leaves out.
const (
	DefaultRosterDir     = "roster"
	DefaultPasswordEnv   = "AUTOBOT_PASSWORD"
	DefaultStagger       = 2 * time.Second
	DefaultMaxConcurrent = 1
	DefaultAttempts      = 3
	DefaultTolerance     = 0.34
)

// Load reads fleet.yml and every roster file it points at.
//
// Unknown YAML keys are an error, exactly as in a profile: a misspelled key that
// silently keeps its default is the kind of bug that only shows up on the live
// server, and a fleet that fields three bots instead of the expected five is a
// slow way to find out.
func Load(path string) (*Fleet, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("fleet: no fleet file given")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fleet: read %s: %w", path, err)
	}

	f := &Fleet{Path: path}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f.Policy); err != nil {
		return nil, fmt.Errorf("fleet: parse %s: %w", path, err)
	}
	f.policyDefaults()

	dir := f.Policy.RosterDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(path), dir)
	}
	files, err := rosterFiles(dir)
	if err != nil {
		// A missing roster directory means there is nothing to run, which is the
		// same answer as an empty one — and naming the directory is what tells the
		// operator which path fleet.yml resolved to.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s does not exist", ErrNoBots, dir)
		}
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no *.yml under %s", ErrNoBots, dir)
	}

	for _, file := range files {
		bots, err := loadRosterFile(file)
		if err != nil {
			return nil, err
		}
		f.Bots = append(f.Bots, bots...)
		f.RosterFiles = append(f.RosterFiles, file)
	}

	f.Apply()
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}

// rosterFiles lists the roster files of a directory in a stable order.
func rosterFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("fleet: read roster dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yml", ".yaml":
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// loadRosterFile reads one roster file, which holds a list of bots.
func loadRosterFile(path string) ([]Bot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fleet: read %s: %w", path, err)
	}

	var doc struct {
		Bots []Bot `yaml:"bots"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("fleet: parse %s: %w", path, err)
	}
	if len(doc.Bots) == 0 {
		return nil, fmt.Errorf("fleet: %s lists no bots", path)
	}
	return doc.Bots, nil
}

// policyDefaults fills in the policy fields fleet.yml left out.
func (f *Fleet) policyDefaults() {
	if f.Policy.RosterDir == "" {
		f.Policy.RosterDir = DefaultRosterDir
	}
	if f.Policy.PasswordEnv == "" {
		f.Policy.PasswordEnv = DefaultPasswordEnv
	}
	if f.Policy.Login.Stagger <= 0 {
		f.Policy.Login.Stagger = DefaultStagger
	}
	if f.Policy.Login.MaxConcurrent <= 0 {
		f.Policy.Login.MaxConcurrent = DefaultMaxConcurrent
	}
	if f.Policy.Login.Attempts <= 0 {
		f.Policy.Login.Attempts = DefaultAttempts
	}
	if f.Policy.Balance.Tolerance <= 0 {
		f.Policy.Balance.Tolerance = DefaultTolerance
	}
}

// namePrefix returns the prefix in the form a character name can carry: letters
// only and in AzerothCore's storage shape.
//
// A prefix may contain digits (it doubles as the account naming scheme, where
// "autobot01" is normal) and a character name may not, so the character carries
// the letters. A prefix without digits is returned unchanged, which is why the
// common case still reads "Autobotalpha".
func namePrefix(prefix string) string {
	return wow.SanitizeName(prefix)
}

// Apply fills in the defaults a roster entry left out: the account name from the
// prefix and the entry's position, and the character name from the label.
//
// Doing it here means nothing downstream has to ask "was this set?", and it keeps
// the generated names in one place so they are stable across runs — a bot that got
// a different account name on the second run would be provisioned twice.
func (f *Fleet) Apply() {
	for i := range f.Bots {
		b := &f.Bots[i]
		if b.Account == "" {
			b.Account = fmt.Sprintf("%s%02d", strings.ToLower(f.Policy.Prefix), i+1)
		}
		if b.Character == "" {
			// The character name carries the prefix's letters: AzerothCore character
			// names may not contain digits, so a prefix like "bot2" cannot be
			// carried literally. The name is the namespace mark all the same — it is
			// what makes a bot's character recognisable without a database join — and
			// SanitizeName applies AC's letter rules and truncates to 12.
			b.Character = wow.SanitizeName(namePrefix(f.Policy.Prefix) + b.Name)
		}
	}
}

// Validate checks the whole fleet before anything is provisioned.
//
// Everything that can be checked is checked here rather than at character
// creation: a roster with a human hunter, or an account outside the prefix, is a
// typo, and finding it out from a server error code mid-run is much more
// expensive than reading it now.
func (f *Fleet) Validate() error {
	var errs []error

	if f.Policy.Prefix == "" {
		errs = append(errs, ErrNoPrefix)
	} else if err := config.ValidPrefix(f.Policy.Prefix); err != nil {
		errs = append(errs, err)
	}
	if f.Policy.AuthAddr == "" {
		errs = append(errs, errors.New("fleet: auth_addr is required"))
	}
	if f.Policy.RealmIndex < 0 {
		errs = append(errs, fmt.Errorf("fleet: realm_index %d must not be negative", f.Policy.RealmIndex))
	}
	if f.Policy.Balance.Tolerance > 1 {
		errs = append(errs, fmt.Errorf("fleet: balance.tolerance %.2f must be between 0 and 1",
			f.Policy.Balance.Tolerance))
	}
	if len(f.Bots) == 0 {
		errs = append(errs, ErrNoBots)
	}

	// seen catches one name used by two bots, which would have them fight over
	// the same account or character.
	seen := map[string]bool{}
	checkUnique := func(kind, value, prefix string) {
		if value == "" {
			errs = append(errs, fmt.Errorf("fleet: a bot has no %s", kind))
			return
		}
		if !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			errs = append(errs, fmt.Errorf("%w: %s %q does not start with %q",
				ErrNotBotName, kind, value, prefix))
		}
		key := kind + ":" + strings.ToLower(value)
		if seen[key] {
			errs = append(errs, fmt.Errorf("fleet: %s %q is used twice", kind, value))
			return
		}
		seen[key] = true
	}

	// Accounts carry the prefix exactly; characters carry its letters, because a
	// character name may not contain digits.
	accountPrefix := f.Policy.Prefix
	characterPrefix := namePrefix(f.Policy.Prefix)

	for _, b := range f.Bots {
		if b.Name == "" {
			errs = append(errs, errors.New("fleet: a roster entry has no name"))
		}
		checkUnique("account", b.Account, accountPrefix)
		checkUnique("character", b.Character, characterPrefix)

		if !config.ValidRaceClass(b.Race, b.Class) {
			errs = append(errs, fmt.Errorf("fleet: %s: AzerothCore cannot create race %d class %d",
				b.Name, b.Race, b.Class))
		}
		if b.Gender > 1 {
			errs = append(errs, fmt.Errorf("fleet: %s: gender %d must be 0 (male) or 1 (female)",
				b.Name, b.Gender))
		}
		if _, err := b.Faction(); err != nil {
			errs = append(errs, fmt.Errorf("fleet: %s: %w", b.Name, err))
		}
	}

	return errors.Join(errs...)
}

// ApplyEnv overlays the environment on the policy, exactly as a profile is
// overlaid: the variables win over the file, so one fleet.yml can be used from a
// laptop and from the machine next to the server without editing it. The host is
// the usual reason — it is the same roster either way.
//
// Both spellings are accepted, AUTOBOT_* first and upstream's E2E_* as the
// fallback, so one .env file drives the CLI and the live tests as it does
// everywhere else in this project.
func (f *Fleet) ApplyEnv(lookup func(string) (string, bool)) error {
	if v, ok := firstEnv(lookup, "AUTOBOT_AUTH_ADDR", "E2E_AUTH_ADDR"); ok {
		f.Policy.AuthAddr = v
	}
	if v, ok := firstEnv(lookup, "AUTOBOT_DATA_DIR"); ok {
		f.Policy.DataDir = v
	}
	return f.Validate()
}

// firstEnv returns the first of names that is set to a non-empty value.
func firstEnv(lookup func(string) (string, bool), names ...string) (string, bool) {
	for _, name := range names {
		if v, ok := lookup(name); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// Password reads the accounts' password from the environment, falling back to
// config.DefaultPassword when the variable is not set.
//
// It is a separate step from Load because a roster may be read for `status` or
// `gc`, neither of which needs a password: making "which bots exist" depend on a
// secret would be an obstacle with no benefit. The fallback is config's, not a
// second copy of it, so the CLI and the fleet can never disagree about what the
// default password is.
func (f *Fleet) Password(lookup func(string) (string, bool)) (string, error) {
	env := f.Policy.PasswordEnv
	if env == "" {
		env = DefaultPasswordEnv
	}
	if v, ok := lookup(env); ok && v != "" {
		return v, nil
	}
	return config.DefaultPassword, nil
}
