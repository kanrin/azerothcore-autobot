package wow

import "testing"

// TestCharDeleteResult pins the SMSG_CHAR_DELETE verdict values and checks that
// each constant has a name, so a log line can never print "UNKNOWN" for a code
// the server really sends.
//
// Source: azerothcore-wotlk/src/server/shared/SharedDefines.h:3650-3654.
func TestCharDeleteResult(t *testing.T) {
	tests := []struct {
		name string
		got  CharDeleteResult
		want uint8
	}{
		{"CHAR_DELETE_SUCCESS", CharDeleteSuccess, 0x47},
		{"CHAR_DELETE_FAILED", CharDeleteFailed, 0x48},
		{"CHAR_DELETE_FAILED_LOCKED_FOR_TRANSFER", CharDeleteFailedLockedForTransfer, 0x49},
		{"CHAR_DELETE_FAILED_GUILD_LEADER", CharDeleteFailedGuildLeader, 0x4A},
		{"CHAR_DELETE_FAILED_ARENA_CAPTAIN", CharDeleteFailedArenaCaptain, 0x4B},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if uint8(tc.got) != tc.want {
				t.Fatalf("value = 0x%02X, want 0x%02X", uint8(tc.got), tc.want)
			}
			if got := tc.got.String(); got != tc.name {
				t.Errorf("String() = %q, want %q", got, tc.name)
			}
			if _, ok := charDeleteResultNames[tc.got]; !ok {
				t.Errorf("%s is missing from charDeleteResultNames", tc.name)
			}
		})
	}

	if got := CharDeleteResult(0x99).String(); got != "CHAR_DELETE_UNKNOWN(0x99)" {
		t.Errorf("unknown code renders as %q, want %q", got, "CHAR_DELETE_UNKNOWN(0x99)")
	}
	if CharDeleteSuccess == CharDeleteFailed {
		t.Fatal("CHAR_DELETE_SUCCESS and CHAR_DELETE_FAILED must not share a value")
	}
}
