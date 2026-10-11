package packet

import "encoding/binary"

// Writer assembles a little-endian packet payload.
//
// All methods are chainable and never fail: the payload is built in memory and
// the caller decides whether the result is well formed.
type Writer struct {
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Bytes returns the assembled payload. The slice aliases the Writer's internal
// buffer, so it must not be modified by the caller.
func (w *Writer) Bytes() []byte { return w.buf }

// Len reports how many bytes have been written.
func (w *Writer) Len() int { return len(w.buf) }

// Uint8 appends one byte.
func (w *Writer) Uint8(v uint8) *Writer {
	w.buf = append(w.buf, v)
	return w
}

// Uint16 appends a little-endian uint16.
func (w *Writer) Uint16(v uint16) *Writer {
	w.buf = binary.LittleEndian.AppendUint16(w.buf, v)
	return w
}

// Uint32 appends a little-endian uint32.
func (w *Writer) Uint32(v uint32) *Writer {
	w.buf = binary.LittleEndian.AppendUint32(w.buf, v)
	return w
}

// Uint64 appends a little-endian uint64.
func (w *Writer) Uint64(v uint64) *Writer {
	w.buf = binary.LittleEndian.AppendUint64(w.buf, v)
	return w
}

// Bool appends one byte, 1 for true and 0 for false. This matches the
// `read<char>() > 0` decoding on the server side.
func (w *Writer) Bool(v bool) *Writer {
	if v {
		return w.Uint8(1)
	}
	return w.Uint8(0)
}

// RawGUID appends guid as a raw little-endian uint64, the encoding used by
// quest, gossip and group packets.
func (w *Writer) RawGUID(guid uint64) *Writer { return w.Uint64(guid) }

// CString appends s followed by a NUL terminator, mirroring AzerothCore's
// `operator<<(std::string_view)` (ByteBuffer.h:212-221).
func (w *Writer) CString(s string) *Writer {
	w.buf = append(w.buf, s...)
	return w.Uint8(0)
}

// Raw appends bytes verbatim. Used for the optional trailing fields that only
// appear for certain packet variants.
func (w *Writer) Raw(b []byte) *Writer {
	w.buf = append(w.buf, b...)
	return w
}
