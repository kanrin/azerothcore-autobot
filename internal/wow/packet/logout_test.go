package packet

import (
	"bytes"
	"errors"
	"testing"
)

// TestParseLogoutResponse pins SMSG_LOGOUT_RESPONSE against the server's writer.
//
// Expected bytes are the literal output of
// AC CharacterPackets.cpp:30-35, LogoutResponse::Write:
//
//	_worldPacket << uint32(LogoutResult);   // little endian
//	_worldPacket << uint8(Instant);
//
// So: 4 bytes of reason, then one byte for the bool. Five bytes total, and the
// order matters — reading Instant first would silently swap a refusal reason
// with an unrelated byte.
func TestParseLogoutResponse(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want LogoutResponse
	}{
		{"accepted, countdown", []byte{0x00, 0x00, 0x00, 0x00, 0x00}, LogoutResponse{Reason: LogoutReasonNone, Instant: false}},
		{"accepted, instant", []byte{0x00, 0x00, 0x00, 0x00, 0x01}, LogoutResponse{Reason: LogoutReasonNone, Instant: true}},
		{"refused: in combat", []byte{0x01, 0x00, 0x00, 0x00, 0x00}, LogoutResponse{Reason: LogoutReasonInCombat, Instant: false}},
		{"refused: falling", []byte{0x03, 0x00, 0x00, 0x00, 0x00}, LogoutResponse{Reason: LogoutReasonFalling, Instant: false}},
		{"refused: blocked, instant flag ignored", []byte{0x02, 0x00, 0x00, 0x00, 0x01}, LogoutResponse{Reason: LogoutReasonBlocked, Instant: true}},
		{"trailing bytes are ignored", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFF}, LogoutResponse{Reason: LogoutReasonNone}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLogoutResponse(tc.data)
			if err != nil {
				t.Fatalf("ParseLogoutResponse(% x) = %v", tc.data, err)
			}
			if got != tc.want {
				t.Errorf("ParseLogoutResponse(% x) = %+v, want %+v", tc.data, got, tc.want)
			}
		})
	}
}

// TestParseLogoutResponse_Truncated makes sure a short packet is reported rather
// than silently decoded from a zero-filled buffer: a wrong logout state is much
// harder to find later than a decode error now.
func TestParseLogoutResponse_Truncated(t *testing.T) {
	for _, data := range [][]byte{nil, {0x00}, {0x00, 0x00, 0x00}, {0x00, 0x00, 0x00, 0x00}} {
		if _, err := ParseLogoutResponse(data); !errors.Is(err, ErrShortBuffer) {
			t.Errorf("ParseLogoutResponse(% x) error = %v, want ErrShortBuffer", data, err)
		}
	}
}

// TestLogoutReasonName keeps the operator-facing text of a refused logout
// unambiguous: the whole point of parsing the reason is to say why.
func TestLogoutReasonName(t *testing.T) {
	tests := []struct {
		reason uint32
		want   string
	}{
		{LogoutReasonNone, "accepted"},
		{LogoutReasonInCombat, "in combat"},
		{LogoutReasonBlocked, "blocked (duel, AFK in a sanctuary, or frozen)"},
		{LogoutReasonFalling, "falling or jumping"},
		{9, "unknown reason 0x9"},
	}
	for _, tc := range tests {
		if got := LogoutReasonName(tc.reason); got != tc.want {
			t.Errorf("LogoutReasonName(%d) = %q, want %q", tc.reason, got, tc.want)
		}
	}
	if got := (LogoutResponse{Reason: LogoutReasonInCombat}).String(); !bytes.Contains([]byte(got), []byte("in combat")) {
		t.Errorf("LogoutResponse.String() = %q, want it to name the reason", got)
	}
}
