package packet

import (
	"errors"
	"testing"
)

// TestCStringIsNotLengthPrefixed pins the encoding rule that is easiest to get
// wrong. AzerothCore's `ByteBuffer::operator>>(std::string&)` calls
// ReadCString(true) (src/server/shared/Packets/ByteBuffer.h:291-295), so a
// string contributes its bytes plus a single NUL — there is no uint32 length
// prefix. An earlier revision of this project's protocol notes claimed a length
// prefix; this test makes that regression impossible to reintroduce.
func TestCStringIsNotLengthPrefixed(t *testing.T) {
	got := NewWriter().CString("ab").Bytes()

	assertBytes(t, "61 62 00", got)
	if len(got) == 6 {
		t.Fatalf("payload looks length-prefixed (uint32 + bytes): % x", got)
	}

	// Decoding must consume exactly what was written and nothing more.
	r := NewReader(got)
	s, err := r.CString()
	if err != nil {
		t.Fatalf("CString: %v", err)
	}
	if s != "ab" {
		t.Fatalf("CString = %q, want %q", s, "ab")
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d byte(s) left after reading the string, want 0", r.Remaining())
	}
}

// TestEmptyCStringIsOneByte documents the shape of the Comment field of a
// CMSG_LFG_JOIN sent without a comment: exactly one NUL.
func TestEmptyCStringIsOneByte(t *testing.T) {
	assertBytes(t, "00", NewWriter().CString("").Bytes())
}

// TestCStringUnterminated: a truncated payload must fail loudly rather than
// silently returning the remainder as the string.
func TestCStringUnterminated(t *testing.T) {
	_, err := NewReader([]byte{'a', 'b'}).CString()
	if !errors.Is(err, ErrUnterminated) {
		t.Fatalf("err = %v, want %v", err, ErrUnterminated)
	}
}

// TestRawGUIDIsEightBytes guards the other easy mistake: the guid fields of
// quest and gossip packets are a raw uint64 (ObjectGuid.cpp:76), not the packed
// form. 0xABC fits in two packed bytes but must still occupy eight.
func TestRawGUIDIsEightBytes(t *testing.T) {
	assertBytes(t, "BC 0A 00 00 00 00 00 00", NewWriter().RawGUID(0xABC).Bytes())
}

// TestPackedGUID keeps the packed form — used by movement packets — distinct
// from RawGUID. For 0xABC the non-zero bytes are at positions 0 and 1, so the
// mask is 0x03 followed by those bytes.
func TestPackedGUID(t *testing.T) {
	r := NewReader([]byte{0x03, 0xBC, 0x0A})
	guid, err := r.PackedGUID()
	if err != nil {
		t.Fatalf("PackedGUID: %v", err)
	}
	if guid != 0xABC {
		t.Fatalf("guid = %#x, want 0xABC", guid)
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", r.Remaining())
	}
}

// TestPackedGUIDAllZeroBytes: a zero guid packs to the single byte 0x00.
func TestPackedGUIDAllZeroBytes(t *testing.T) {
	r := NewReader([]byte{0x00})
	guid, err := r.PackedGUID()
	if err != nil {
		t.Fatalf("PackedGUID: %v", err)
	}
	if guid != 0 {
		t.Fatalf("guid = %#x, want 0", guid)
	}
}

// TestReaderShortBuffer ensures every primitive fails with ErrShortBuffer
// instead of panicking or returning zero values.
func TestReaderShortBuffer(t *testing.T) {
	tests := []struct {
		name string
		run  func(r *Reader) error
	}{
		{"uint8", func(r *Reader) error { _, err := r.Uint8(); return err }},
		{"uint16", func(r *Reader) error { _, err := r.Uint16(); return err }},
		{"uint32", func(r *Reader) error { _, err := r.Uint32(); return err }},
		{"uint64", func(r *Reader) error { _, err := r.Uint64(); return err }},
		{"bool", func(r *Reader) error { _, err := r.Bool(); return err }},
		{"raw guid", func(r *Reader) error { _, err := r.RawGUID(); return err }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(NewReader(nil)); !errors.Is(err, ErrShortBuffer) {
				t.Fatalf("err = %v, want %v", err, ErrShortBuffer)
			}
		})
	}
}

// TestReaderPackedGUIDShortMask: a mask promising more bytes than the payload
// holds must fail rather than return a partly assembled guid.
func TestReaderPackedGUIDShortMask(t *testing.T) {
	if _, err := NewReader([]byte{0xFF, 0x01}).PackedGUID(); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("err = %v, want %v", err, ErrShortBuffer)
	}
}

// TestWriterReaderRoundTrip checks that the writer and reader agree on every
// primitive. This does not pin the wire format (the golden tests above do) but
// it does catch a mismatched width between the two implementations.
func TestWriterReaderRoundTrip(t *testing.T) {
	const guid = 0x1122334455667788

	payload := NewWriter().
		Uint8(0xA1).
		Uint16(0xB2C3).
		Uint32(0xD4E5F607).
		Uint64(guid).
		Bool(true).
		Bool(false).
		RawGUID(guid).
		CString("hello").
		Bytes()

	r := NewReader(payload)

	if v, err := r.Uint8(); err != nil || v != 0xA1 {
		t.Fatalf("Uint8 = %#x, %v; want 0xA1, nil", v, err)
	}
	if v, err := r.Uint16(); err != nil || v != 0xB2C3 {
		t.Fatalf("Uint16 = %#x, %v; want 0xB2C3, nil", v, err)
	}
	if v, err := r.Uint32(); err != nil || v != 0xD4E5F607 {
		t.Fatalf("Uint32 = %#x, %v; want 0xD4E5F607, nil", v, err)
	}
	if v, err := r.Uint64(); err != nil || v != guid {
		t.Fatalf("Uint64 = %#x, %v; want %#x, nil", v, err, guid)
	}
	if v, err := r.Bool(); err != nil || !v {
		t.Fatalf("Bool = %v, %v; want true, nil", v, err)
	}
	if v, err := r.Bool(); err != nil || v {
		t.Fatalf("Bool = %v, %v; want false, nil", v, err)
	}
	if v, err := r.RawGUID(); err != nil || v != guid {
		t.Fatalf("RawGUID = %#x, %v; want %#x, nil", v, err, guid)
	}
	if v, err := r.CString(); err != nil || v != "hello" {
		t.Fatalf("CString = %q, %v; want \"hello\", nil", v, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", r.Remaining())
	}
}

// TestBoolIsOneByte checks the width directly: AzerothCore decodes a bool with
// `read<char>() > 0` (ByteBuffer.h:233), so true must be one byte, not four.
func TestBoolIsOneByte(t *testing.T) {
	assertBytes(t, "01", NewWriter().Bool(true).Bytes())
	assertBytes(t, "00", NewWriter().Bool(false).Bytes())
}
