package packet

import "testing"

// Golden bytes for the gossip CMSG encoders. AzerothGhost v1.0.9 implements
// none of these, so the expectations come from MiscHandler.cpp:98 and
// NPCHandler.cpp:142 plus the handler table in Opcodes.cpp.
func TestEncodeGossipHello(t *testing.T) {
	// AC NPCHandler.cpp:142 — `recvData >> guid`, registered at Opcodes.cpp:510.
	assertBytes(t, "BC 0A 00 00 00 00 00 00", EncodeGossipHello(0x0000000000000ABC))
}

// TestEncodeGossipSelectOption covers both variants. AC MiscHandler.cpp:98-107
// reads guid, menuId and gossipListId, and only then — when the selected option
// is flagged as coded — a CString. The plain variant must therefore end right
// after gossipListId; an extra NUL would be harmless, but a missing code field
// on a coded menu desynchronises the server's read offset.
func TestEncodeGossipSelectOption(t *testing.T) {
	const guid = 0x0000000000000ABC

	t.Run("plain option ends after gossipListId", func(t *testing.T) {
		got := EncodeGossipSelectOption(guid, 0x1234, 2, "")
		assertBytes(t, "BC 0A 00 00 00 00 00 00"+
			" 34 12 00 00"+
			" 02 00 00 00",
			got)
		if len(got) != 16 {
			t.Fatalf("payload = %d bytes, want 16: % x", len(got), got)
		}
	})

	t.Run("coded option appends a cstring", func(t *testing.T) {
		assertBytes(t, "BC 0A 00 00 00 00 00 00"+
			" 34 12 00 00"+
			" 02 00 00 00"+
			" 61 62 63 00",
			EncodeGossipSelectOption(guid, 0x1234, 2, "abc"))
	})
}

// TestEncodeNpcTextQuery: AC QueryHandler.cpp:279-282 reads a uint32 text id
// followed by a raw guid — note the unusual order, id first.
func TestEncodeNpcTextQuery(t *testing.T) {
	assertBytes(t, "34 12 00 00"+
		" BC 0A 00 00 00 00 00 00",
		EncodeNpcTextQuery(0x1234, 0x0000000000000ABC))
}

// TestEncodeQuestgiverStatusMultipleQueryIsEmpty: AC QuestHandler.cpp:620-623
// takes the packet but reads nothing, so any payload would be ignored — but
// sending zero bytes is the faithful reproduction of the 3.3.5a client.
func TestEncodeQuestgiverStatusMultipleQueryIsEmpty(t *testing.T) {
	if got := EncodeQuestgiverStatusMultipleQuery(); len(got) != 0 {
		t.Fatalf("payload = % x, want empty", got)
	}
}
