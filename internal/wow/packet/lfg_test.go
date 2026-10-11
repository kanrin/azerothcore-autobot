package packet

import "testing"

// randomDungeonSlot is a random-dungeon entry: type 6 (LfgTypeRandom) in the
// high byte, dungeon id 1 in the low 24 bits.
const randomDungeonSlot = 0x06000001

// Golden bytes for the LFG CMSG encoders, transcribed from LFGPackets.cpp
// (LFGJoin::Read) and LFGHandler.cpp. AzerothGhost has no LFG packet code at
// all, so these expectations are the only wire-format record this project has.
func TestEncodeLfgJoin(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{
			// Roles=2 (tank) as uint32 | NoPartialClear=0 | Achievements=0 |
			// 1 slot | slot 0x06000001 little-endian | needs count 3 |
			// needs 1,1,3 | empty comment CString. 16 bytes total.
			name: "tank, one random dungeon, no comment",
			got: EncodeLfgJoin(0x02,
				[]uint32{randomDungeonSlot},
				[LfgNeedsCount]uint8{1, 1, 3}, ""),
			want: "02 00 00 00" +
				" 00" +
				" 00" +
				" 01" +
				" 01 00 00 06" +
				" 03" +
				" 01 01 03" +
				" 00",
		},
		{
			// Same, but with a comment: the CString contributes its bytes plus
			// one NUL and NO length prefix.
			name: "healer with a comment",
			got: EncodeLfgJoin(0x04,
				[]uint32{randomDungeonSlot},
				[LfgNeedsCount]uint8{1, 1, 3}, "lfm"),
			want: "04 00 00 00" +
				" 00" +
				" 00" +
				" 01" +
				" 01 00 00 06" +
				" 03" +
				" 01 01 03" +
				" 6C 66 6D 00",
		},
		{
			// Two slots: the count byte grows and both uint32 entries follow.
			// 4 + 1 + 1 + 1 + 4 + 4 + 1 + 3 + 1 = 20 bytes.
			name: "damage, two explicit dungeons",
			got: EncodeLfgJoin(0x08,
				[]uint32{0x01000022, 0x01000024},
				[LfgNeedsCount]uint8{1, 1, 3}, ""),
			want: "08 00 00 00" +
				" 00" +
				" 00" +
				" 02" +
				" 22 00 00 01" +
				" 24 00 00 01" +
				" 03" +
				" 01 01 03" +
				" 00",
		},
		{
			// An empty slot list is rejected by the server without a reply
			// (LFGHandler.cpp:57-61); the encoder still produces the faithful
			// bytes so a caller cannot accidentally send a malformed packet.
			name: "no slots still encodes the count byte",
			got: EncodeLfgJoin(0x02, nil,
				[LfgNeedsCount]uint8{1, 1, 3}, ""),
			want: "02 00 00 00" +
				" 00" +
				" 00" +
				" 00" +
				" 03" +
				" 01 01 03" +
				" 00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertBytes(t, tc.want, tc.got)
		})
	}
}

// TestEncodeLfgJoinFieldWidths asserts the byte offsets directly, so a future
// reordering of the struct fields fails loudly instead of shifting everything.
func TestEncodeLfgJoinFieldWidths(t *testing.T) {
	got := EncodeLfgJoin(0x02, []uint32{randomDungeonSlot},
		[LfgNeedsCount]uint8{1, 1, 3}, "")

	if len(got) != 16 {
		t.Fatalf("payload = %d bytes, want 16: % x", len(got), got)
	}
	if got[4] != 0x00 || got[5] != 0x00 {
		t.Errorf("NoPartialClear/Achievements bytes = % x, want 00 00", got[4:6])
	}
	if got[6] != 0x01 {
		t.Errorf("slot count byte = %#x, want 0x01", got[6])
	}
	if got[11] != 0x03 {
		t.Errorf("needs count byte = %#x, want 0x03", got[11])
	}
	if got[15] != 0x00 {
		t.Errorf("comment terminator = %#x, want 0x00", got[15])
	}
}

// TestEncodeLfgProposalResult: AC LFGHandler.cpp:99-100 reads a uint32 proposal
// id and a bool. The bool is one byte, not four.
func TestEncodeLfgProposalResult(t *testing.T) {
	assertBytes(t, "44 33 22 11 01", EncodeLfgProposalResult(0x11223344, true))
	assertBytes(t, "44 33 22 11 00", EncodeLfgProposalResult(0x11223344, false))
}

// TestEncodeSingleFieldLfgPackets covers the packets whose whole body is one
// small field, plus the two that carry no body at all.
func TestEncodeSingleFieldLfgPackets(t *testing.T) {
	t.Run("LfgLeave is empty", func(t *testing.T) {
		// AC LFGHandler.cpp:78-80 ignores the body.
		if got := EncodeLfgLeave(); len(got) != 0 {
			t.Fatalf("payload = % x, want empty", got)
		}
	})
	t.Run("LfgGetStatus is empty", func(t *testing.T) {
		// AC LFGHandler.cpp:281-291 ignores the body.
		if got := EncodeLfgGetStatus(); len(got) != 0 {
			t.Fatalf("payload = % x, want empty", got)
		}
	})
	t.Run("LfgSetRoles is one byte", func(t *testing.T) {
		assertBytes(t, "04", EncodeLfgSetRoles(0x04))
	})
	t.Run("LfgSetBootVote is one byte", func(t *testing.T) {
		assertBytes(t, "01", EncodeLfgSetBootVote(true))
		assertBytes(t, "00", EncodeLfgSetBootVote(false))
	})
	t.Run("LfgTeleport is one byte", func(t *testing.T) {
		assertBytes(t, "01", EncodeLfgTeleport(true))
		assertBytes(t, "00", EncodeLfgTeleport(false))
	})
	t.Run("SetLfgComment is a cstring", func(t *testing.T) {
		// AC LFGHandler.cpp:122-125; opcode 0x366, registered at Opcodes.cpp:1001.
		assertBytes(t, "61 62 00", EncodeSetLfgComment("ab"))
		assertBytes(t, "00", EncodeSetLfgComment(""))
	})
}
