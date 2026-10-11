package wow

import (
	"strings"
	"testing"
)

// The rules below are AzerothCore's, not ours: the 2..12 length check
// (CHAR_NAME_TOO_SHORT / CHAR_NAME_TOO_LONG), the storage form (first letter
// upper, rest lower), and the three-consecutive-letters check that answers
// CHAR_NAME_THREE_CONSECUTIVE. Getting any of them wrong costs a round trip to
// learn that the server said no.
func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "already canonical", in: "Thrall", want: "Thrall"},
		{name: "lowercase", in: "thrall", want: "Thrall"},
		{name: "uppercase", in: "THRALL", want: "Thrall"},
		{name: "digits dropped", in: "thr4ll", want: "Thrll"},
		{name: "punctuation dropped", in: "t-h_r.ll", want: "Thrll"},
		{name: "spaces dropped", in: " Thrall Bot ", want: "Thrallbot"},
		{name: "non ascii dropped", in: "爪Thrall爪", want: "Thrall"},
		{name: "only non letters falls back", in: "12345", want: "Bot"},
		{name: "empty falls back", in: "", want: "Bot"},
		{name: "one letter is padded", in: "a", want: "Bota"},

		// A triple is bumped on its third letter, so the name stops at two in a
		// row and the bump can never create a new triple behind it.
		{name: "triple a", in: "aaabc", want: "Aabbc"},
		{name: "triple z wraps to a", in: "zzz", want: "Zza"},
		{name: "quadruple", in: "aaaab", want: "Aabab"},

		{name: "truncated to twelve", in: "Abcdefghijklmn", want: "Abcdefghijkl"},
		{name: "truncation keeps canonical form", in: "ABCDEFGHIJKLM", want: "Abcdefghijkl"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeName(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// SanitizeName must be idempotent: the caller compares its result against the
// character list, and a second pass that changed anything would mean the name it
// looks for is not the name the server stored.
func TestSanitizeNameIsIdempotent(t *testing.T) {
	inputs := []string{
		"Thrall", "aaabc", "aaaab", "zzz", "Abcdefghijklmn", "", "12345", "a",
		"t-h_r.ll", "ABCDEFGHIJKLM", "Zzaaa", "q", "xTzQpLmNvBcDe",
	}
	for _, in := range inputs {
		once := SanitizeName(in)
		twice := SanitizeName(once)
		if once != twice {
			t.Errorf("SanitizeName(%q) = %q, then SanitizeName(%q) = %q; must be idempotent",
				in, once, once, twice)
		}
	}
}

// Every result must satisfy the server's rules, for every input. This is the
// property the create loop depends on: an invalid candidate wastes a round trip
// and can never succeed, however many times it is retried.
func TestSanitizeNameAlwaysProducesAcceptableName(t *testing.T) {
	inputs := []string{
		"", "a", "ab", "Thrall", "aaabc", "zzz", "12345", "-", "日本語",
		"Abcdefghijklmnop", "xXxXxXxXxXxX", "aaa", "zzzzz", "qqqq", "Thrall!!",
	}
	for _, in := range inputs {
		assertAcceptableName(t, in, SanitizeName(in))
	}
}

func TestGenerateNames(t *testing.T) {
	t.Run("preferred first then variants", func(t *testing.T) {
		names := GenerateNames("Thrall", 3)
		if len(names) != 4 {
			t.Fatalf("got %d names, want 4: %v", len(names), names)
		}
		if names[0] != "Thrall" {
			t.Fatalf("first candidate = %q, want %q", names[0], "Thrall")
		}

		seen := make(map[string]bool, len(names))
		for _, n := range names {
			if seen[n] {
				t.Errorf("duplicate candidate %q in %v", n, names)
			}
			seen[n] = true
			assertAcceptableName(t, "generated from Thrall", n)
		}
	})

	t.Run("extra zero yields only the preferred name", func(t *testing.T) {
		names := GenerateNames("Thrall", 0)
		if len(names) != 1 || names[0] != "Thrall" {
			t.Fatalf("got %v, want [Thrall]", names)
		}
	})

	t.Run("negative extra is clamped", func(t *testing.T) {
		names := GenerateNames("Thrall", -5)
		if len(names) != 1 || names[0] != "Thrall" {
			t.Fatalf("got %v, want [Thrall]", names)
		}
	})

	t.Run("long base leaves room for the suffix", func(t *testing.T) {
		// The base is truncated before the suffix is appended, so a variant is
		// exactly the maximum length and its prefix still identifies the bot.
		names := GenerateNames("Abcdefghijkl", 2)
		if len(names) != 3 {
			t.Fatalf("got %d names, want 3: %v", len(names), names)
		}
		for _, n := range names[1:] {
			if !strings.HasPrefix(n, "Abcdefgh") {
				t.Errorf("variant %q does not start with the truncated base %q", n, "Abcdefgh")
			}
			assertAcceptableName(t, "long base", n)
		}
	})

	t.Run("empty preferred name still yields a usable name", func(t *testing.T) {
		names := GenerateNames("", 2)
		if names[0] != "Bot" {
			t.Fatalf("first candidate = %q, want %q", names[0], "Bot")
		}
		for _, n := range names {
			assertAcceptableName(t, "empty preferred", n)
		}
	})
}

func TestLetterSuffix(t *testing.T) {
	tests := []struct {
		n     uint64
		width int
		want  string
	}{
		{n: 0, width: 4, want: "aaaa"},
		{n: 1, width: 4, want: "aaab"},
		{n: 25, width: 4, want: "aaaz"},
		{n: 26, width: 4, want: "aaba"},
		{n: 675, width: 4, want: "aazz"},
		{n: 1, width: 1, want: "b"},
	}
	for _, tc := range tests {
		if got := letterSuffix(tc.n, tc.width); got != tc.want {
			t.Errorf("letterSuffix(%d, %d) = %q, want %q", tc.n, tc.width, got, tc.want)
		}
	}
}

// assertAcceptableName checks the server-side rules a generated name must meet.
// It reports the input too, so a failure names the case that produced the name.
func assertAcceptableName(t *testing.T, input, name string) {
	t.Helper()

	if len(name) < charNameMinLen || len(name) > charNameMaxLen {
		t.Errorf("name %q (from %q) is %d characters, want %d..%d",
			name, input, len(name), charNameMinLen, charNameMaxLen)
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			t.Errorf("name %q (from %q) has non-letter byte %q at %d", name, input, c, i)
		}
	}

	if len(name) > 0 && (name[0] < 'A' || name[0] > 'Z') {
		t.Errorf("name %q (from %q) does not start upper-case", name, input)
	}
	for i := 1; i < len(name); i++ {
		if name[i] >= 'A' && name[i] <= 'Z' {
			t.Errorf("name %q (from %q) has upper-case byte at %d after the first", name, input, i)
		}
	}

	for i := 2; i < len(name); i++ {
		if name[i] == name[i-1] && name[i] == name[i-2] {
			t.Errorf("name %q (from %q) has three consecutive %q ending at %d", name, input, name[i], i)
		}
	}
}
