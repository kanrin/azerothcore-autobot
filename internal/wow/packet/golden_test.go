package packet

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// mustHex decodes a hex string transcribed by hand from the AzerothCore C++.
//
// All whitespace is stripped, so a literal can be indented inside a table without
// the indentation becoming part of the expectation.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatalf("bad hex in test expectation %q: %v", s, err)
	}
	return b
}

// assertBytes compares got against a hex-encoded expectation.
//
// Every expectation in this package is a literal written out from the
// AzerothCore Read() (or from a captured client packet); none of them may be
// produced by running our own encoder, or the test would only prove that the
// encoder agrees with itself.
func assertBytes(t *testing.T, wantHex string, got []byte) {
	t.Helper()
	want := mustHex(t, wantHex)
	if !bytes.Equal(got, want) {
		t.Errorf("payload mismatch\n  want (%2d bytes) % x\n  got  (%2d bytes) % x", len(want), want, len(got), got)
	}
}
