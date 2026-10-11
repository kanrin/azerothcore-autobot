package wow

import (
	"testing"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// Characters are matched by name and never by position. Logging in chars[0]
// instead would silently play a different character than the caller asked for —
// and on a shared account that can mean playing someone else's.
func TestFindCharacter(t *testing.T) {
	chars := []client.CharEnumEntry{
		{GUID: 1, Name: "Thrall", Race: 2, Class: 1, Level: 10},
		{GUID: 2, Name: "Jaina", Race: 1, Class: 8, Level: 20},
	}

	t.Run("exact match", func(t *testing.T) {
		got, ok := findCharacter(chars, "Jaina")
		if !ok {
			t.Fatal("Jaina not found")
		}
		if got.GUID != 2 || got.Name != "Jaina" || got.Race != 1 || got.Class != 8 || got.Level != 20 {
			t.Fatalf("got %+v, want guid=2 name=Jaina race=1 class=8 level=20", got)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		for _, name := range []string{"jaina", "JAINA", "jAiNa"} {
			got, ok := findCharacter(chars, name)
			if !ok {
				t.Fatalf("%q not found", name)
			}
			if got.GUID != 2 {
				t.Fatalf("%q matched guid=%d, want 2", name, got.GUID)
			}
		}
	})

	t.Run("missing name", func(t *testing.T) {
		if _, ok := findCharacter(chars, "Arthas"); ok {
			t.Fatal("Arthas should not be found")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		if _, ok := findCharacter(nil, "Thrall"); ok {
			t.Fatal("nothing should be found in an empty list")
		}
	})

	t.Run("prefix is not a match", func(t *testing.T) {
		// A prefix match would return the wrong character and then fail the login
		// in a way that looks like a protocol problem.
		if _, ok := findCharacter(chars, "Thra"); ok {
			t.Fatal("a prefix must not match")
		}
	})
}

func TestEnterWorldOptionsDefaults(t *testing.T) {
	got := EnterWorldOptions{}.withDefaults()

	if got.NameAttempts != 5 {
		t.Errorf("NameAttempts = %d, want 5", got.NameAttempts)
	}
	if got.CharEnumTimeout != 15*time.Second {
		t.Errorf("CharEnumTimeout = %v, want %v", got.CharEnumTimeout, 15*time.Second)
	}
	if got.CharCreateTimeout != 20*time.Second {
		t.Errorf("CharCreateTimeout = %v, want %v", got.CharCreateTimeout, 20*time.Second)
	}
	if got.LoginTimeout != 60*time.Second {
		t.Errorf("LoginTimeout = %v, want %v", got.LoginTimeout, 60*time.Second)
	}
	if got.Appearance != DefaultAppearance(0) {
		t.Errorf("Appearance = %+v, want the default for gender 0", got.Appearance)
	}
	if got.Logf == nil {
		t.Error("Logf must default to a non-nil discarding logger")
	}
	// The discarding logger must be callable without a nil dereference.
	got.Logf("no %s here", "op")
}

func TestEnterWorldOptionsKeepsExplicitValues(t *testing.T) {
	got := EnterWorldOptions{
		NameAttempts:      2,
		CharEnumTimeout:   3 * time.Second,
		CharCreateTimeout: 4 * time.Second,
		LoginTimeout:      5 * time.Second,
		Appearance:        Appearance{Gender: 1, Skin: 2, Face: 3, HairStyle: 4, HairColor: 5, FacialHair: 6, OutfitID: 7},
	}.withDefaults()

	if got.NameAttempts != 2 || got.CharEnumTimeout != 3*time.Second ||
		got.CharCreateTimeout != 4*time.Second || got.LoginTimeout != 5*time.Second {
		t.Fatalf("explicit timeouts were overwritten: %+v", got)
	}
	if got.Appearance.HairColor != 5 || got.Appearance.OutfitID != 7 {
		t.Fatalf("explicit appearance was overwritten: %+v", got.Appearance)
	}
}

// A zero Appearance means "not specified" and is replaced by the default. That
// is only safe because no real appearance is all-zero: hair colour 0 exists but
// skin/face/hair indices start at 1 in the character creation data.
func TestDefaultAppearance(t *testing.T) {
	got := DefaultAppearance(1)
	want := Appearance{Gender: 1, Skin: 1, Face: 1, HairStyle: 1, HairColor: 0, FacialHair: 1, OutfitID: 0}
	if got != want {
		t.Fatalf("DefaultAppearance(1) = %+v, want %+v", got, want)
	}
	if got == (Appearance{}) {
		t.Fatal("the default must not be the zero value, or withDefaults cannot detect 'unset'")
	}
}
