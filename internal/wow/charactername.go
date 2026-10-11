package wow

import (
	"strings"
	"time"
)

// Character name rules enforced by AzerothCore.
//
// Source: azerothcore-wotlk/src/server/game/Globals/ObjectMgr.cpp:9293-9329
// (ObjectMgr::CheckPlayerName), which is what answers SMSG_CHAR_CREATE.
//
//   - Length 2..12: MAX_PLAYER_NAME is 12 (ObjectMgr.h:687) and
//     CONFIG_MIN_PLAYER_NAME defaults to 2 (worldserver.conf.dist:1912), giving
//     CHAR_NAME_TOO_SHORT / CHAR_NAME_TOO_LONG.
//   - Letters only, under the default CONFIG_STRICT_PLAYER_NAMES = 1: a digit
//     fails isValidString and comes back as CHAR_NAME_MIXED_LANGUAGES (0x5D), a
//     misleadingly named code that reads like an encoding problem but means
//     "invalid character in name".
//   - Three identical letters in a row (compared case-insensitively) fail with
//     CHAR_NAME_THREE_CONSECUTIVE (0x62). This is the rule that bites when
//     generating names by appending a suffix to a base that already ends in the
//     same letter.
//   - Storage form is first letter upper, rest lower. The server normalises on
//     create, so a generated name must already look like that or matching it
//     against a character list by name will fail.
const (
	charNameMinLen = 2
	charNameMaxLen = 12

	// charNameFallback prefixes names whose sanitised form would be too short to
	// be accepted at all.
	charNameFallback = "Bot"

	// charNameSuffixWidth is the width of the base-26 uniqueness suffix. Four
	// letters give 456,976 combinations, which is far more than the handful of
	// bots this project runs, and keeps a generated name at most
	// charNameMaxLen characters even for a full-length base.
	charNameSuffixWidth = 4
)

// SanitizeName turns an arbitrary string into a name AzerothCore will accept.
//
// Non-ASCII and non-letter runes are dropped rather than replaced: substituting
// a character would silently invent a name the caller did not ask for, and the
// caller is about to compare the result against an existing character list.
func SanitizeName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			b.WriteByte(c)
		}
	}
	out := b.String()

	if len(out) > charNameMaxLen {
		out = out[:charNameMaxLen]
	}
	if len(out) < charNameMinLen {
		out = charNameFallback + out
		if len(out) > charNameMaxLen {
			out = out[:charNameMaxLen]
		}
	}
	return normalizeName(out)
}

// normalizeName applies the storage form: everything lower-case except the first
// letter, and no three identical letters in a row.
//
// The order matters and is taken from the server, ObjectMgr.cpp:9318-9320
// (ObjectMgr::CheckPlayerName): AC lower-cases the *whole* name and only then
// scans for `wname[i] == wname[i-1] == wname[i-2]`. Upper-casing the first letter
// before the scan would hide a triple starting at index 0 — "aaabc" would come
// back as "Aaabc" and be rejected with CHAR_NAME_THREE_CONSECUTIVE, so the create
// loop would burn a name attempt on a candidate that can never be accepted.
func normalizeName(s string) string {
	b := []byte(s)

	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}

	// The bump can never create a new triple: before it, b[i] == b[i-2], so the
	// bumped b[i] differs from b[i-1], and the next iteration compares against
	// b[i-1] and b[i-2], which the bump did not touch.
	for i := 2; i < len(b); i++ {
		if b[i] == b[i-1] && b[i] == b[i-2] {
			if b[i] == 'z' {
				b[i] = 'a'
			} else {
				b[i]++
			}
		}
	}

	if len(b) > 0 && b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// GenerateNames returns a list of candidate names to try in order: the preferred
// name first, then `extra` variants built by appending a time-seeded base-26
// suffix.
//
// Callers walk the list until the server reports CHAR_CREATE_SUCCESS. Nothing
// here checks whether a name is free — only the server can know that, and
// pre-checking the database would race with other players creating characters.
func GenerateNames(preferred string, extra int) []string {
	base := SanitizeName(preferred)
	if extra < 0 {
		extra = 0
	}

	out := make([]string, 0, extra+1)
	out = append(out, base)

	// Leave room for the suffix so the result never gets truncated back into a
	// collision with another candidate.
	prefix := base
	if max := charNameMaxLen - charNameSuffixWidth; len(prefix) > max {
		prefix = prefix[:max]
	}

	seed := uint64(time.Now().UnixNano())
	for i := 0; i < extra; i++ {
		out = append(out, SanitizeName(prefix+letterSuffix(seed+uint64(i), charNameSuffixWidth)))
	}
	return out
}

// letterSuffix renders n as a fixed-width base-26 lower-case letter string.
func letterSuffix(n uint64, width int) string {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte('a' + n%26)
		n /= 26
	}
	return string(out)
}
