// Package packet implements the byte-level codec for World of Warcraft 3.3.5a
// client packets.
//
// Every layout here is derived from the AzerothCore server source, and each
// encoder cites the exact Read() it mirrors. The wire format is pinned by
// golden-byte tests in this package: expected bytes are written out by hand
// from the C++ and must never be regenerated from our own encoder.
//
// Two encoding rules are easy to get wrong and are therefore explicit:
//
//   - GUIDs in quest/gossip/group packets are a *raw* little-endian uint64, not
//     the packed form used by movement packets. See Reader.RawGUID.
//   - Strings read with `>> std::string` are NUL-terminated C strings, not
//     length-prefixed. See Reader.CString and Writer.CString.
package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrShortBuffer is returned when a read runs past the end of the payload.
var ErrShortBuffer = errors.New("packet: short buffer")

// ErrUnterminated is returned when a C string is not NUL-terminated before the
// end of the payload.
var ErrUnterminated = errors.New("packet: unterminated cstring")

// Reader decodes little-endian primitives from a packet payload.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	buf []byte
	pos int
}

// NewReader wraps buf. The payload is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Pos reports the current read offset.
func (r *Reader) Pos() int { return r.pos }

// Uint8 reads one byte.
func (r *Reader) Uint8() (uint8, error) {
	if r.Remaining() < 1 {
		return 0, fmt.Errorf("packet: uint8 at %d: %w", r.pos, ErrShortBuffer)
	}
	v := r.buf[r.pos]
	r.pos++
	return v, nil
}

// Uint16 reads a little-endian uint16.
func (r *Reader) Uint16() (uint16, error) {
	if r.Remaining() < 2 {
		return 0, fmt.Errorf("packet: uint16 at %d: %w", r.pos, ErrShortBuffer)
	}
	v := binary.LittleEndian.Uint16(r.buf[r.pos:])
	r.pos += 2
	return v, nil
}

// Uint32 reads a little-endian uint32.
func (r *Reader) Uint32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, fmt.Errorf("packet: uint32 at %d: %w", r.pos, ErrShortBuffer)
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// Uint64 reads a little-endian uint64.
func (r *Reader) Uint64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, fmt.Errorf("packet: uint64 at %d: %w", r.pos, ErrShortBuffer)
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// Bool reads a single byte.
//
// AzerothCore's ByteBuffer::operator>>(bool&) is `value = read<char>() > 0`
// (src/server/shared/Packets/ByteBuffer.h:233), so a bool is exactly one byte
// and any non-zero value means true.
func (r *Reader) Bool() (bool, error) {
	v, err := r.Uint8()
	if err != nil {
		return false, err
	}
	return v > 0, nil
}

// RawGUID reads an ObjectGuid in the raw encoding used by quest, gossip and
// group packets.
//
// AzerothCore's `operator>>(ByteBuffer&, ObjectGuid&)` is
// `guid.Set(buf.read<uint64>())`
// (src/server/game/Entities/Object/ObjectGuid.cpp:76) — a plain little-endian
// uint64. Movement packets use a different, packed encoding; use PackedGUID for
// those.
func (r *Reader) RawGUID() (uint64, error) {
	return r.Uint64()
}

// PackedGUID reads the packed ObjectGuid form: a bitmask byte followed by the
// non-zero bytes of the guid, least significant first.
//
// Only movement and object-query packets use this form.
func (r *Reader) PackedGUID() (uint64, error) {
	mask, err := r.Uint8()
	if err != nil {
		return 0, err
	}
	var guid uint64
	for i := 0; i < 8; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		b, err := r.Uint8()
		if err != nil {
			return 0, err
		}
		guid |= uint64(b) << (8 * uint(i))
	}
	return guid, nil
}

// CString reads a NUL-terminated string.
//
// AzerothCore's `ByteBuffer::operator>>(std::string&)` calls ReadCString(true)
// (src/server/shared/Packets/ByteBuffer.h:291-295). It is NOT length-prefixed.
// Getting this wrong is silent and fatal: the server consumes the two extra
// length bytes as payload and every following field is garbage.
func (r *Reader) CString() (string, error) {
	start := r.pos
	for r.pos < len(r.buf) {
		if r.buf[r.pos] == 0 {
			s := string(r.buf[start:r.pos])
			r.pos++
			return s, nil
		}
		r.pos++
	}
	return "", fmt.Errorf("packet: cstring at %d: %w", start, ErrUnterminated)
}
