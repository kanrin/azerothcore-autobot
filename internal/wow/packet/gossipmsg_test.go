package packet

import (
	"errors"
	"testing"
)

// TestParseGossipMessage uses golden bytes transcribed from GossipDef.cpp:197-234.
func TestParseGossipMessage(t *testing.T) {
	payload := mustHex(t, `
		BC 0A 00 00 00 00 00 00
		2C DE 00 00
		12 F4 00 00
		02 00 00 00
		00 00 00 00
		00
		00
		00 00 00 00
		49 20 68 61 76 65 20 61 20 71 75 65 73 74 00
		00
		01 00 00 00
		06
		01
		64 00 00 00
		50 61 79 00
		31 30 30 20 63 6F 70 70 65 72 00
		01 00 00 00
		0F 03 00 00
		02 00 00 00
		01 00 00 00
		00 00 00 00
		00
		41 20 54 68 72 65 61 74 20 57 69 74 68 69 6E 00
	`)

	message, err := ParseGossipMessage(payload)
	if err != nil {
		t.Fatalf("ParseGossipMessage: %v", err)
	}
	if message.GiverGUID != 0xABC {
		t.Errorf("GiverGUID = %#x, want 0xABC", message.GiverGUID)
	}
	if message.MenuID != 0xDE2C || message.TextID != 0xF412 {
		t.Errorf("menu/text = %#x/%#x, want 0xDE2C/0xF412", message.MenuID, message.TextID)
	}
	if len(message.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(message.Options))
	}

	first := message.Options[0]
	if first.ListID != 0 || first.Icon != 0 || first.Coded || first.BoxMoney != 0 {
		t.Errorf("option 0 = %+v, want a plain visible option", first)
	}
	if first.Message != "I have a quest" || first.BoxMessage != "" {
		t.Errorf("option 0 text = %q/%q", first.Message, first.BoxMessage)
	}

	second := message.Options[1]
	wantOption := GossipOption{ListID: 1, Icon: 6, Coded: true, BoxMoney: 100,
		Message: "Pay", BoxMessage: "100 copper"}
	if second != wantOption {
		t.Errorf("option 1 = %+v, want %+v", second, wantOption)
	}

	if len(message.Quests) != 1 {
		t.Fatalf("quest menu = %d entries, want 1", len(message.Quests))
	}
	want := QuestListEntry{ID: 783, Icon: 2, Level: 1, Title: "A Threat Within"}
	if message.Quests[0] != want {
		t.Errorf("quest entry = %+v, want %+v", message.Quests[0], want)
	}
}

// TestParseGossipMessage_Empty covers the shape a non-gossip creature answers with:
// no options and no quests. It must parse, because "the NPC has nothing" is a normal
// answer, not a protocol error.
func TestParseGossipMessage_Empty(t *testing.T) {
	message, err := ParseGossipMessage(mustHex(t, `
		01 00 00 00 00 00 00 00
		00 00 00 00
		00 00 00 00
		00 00 00 00
		00 00 00 00
	`))
	if err != nil {
		t.Fatalf("ParseGossipMessage: %v", err)
	}
	if len(message.Options) != 0 || len(message.Quests) != 0 {
		t.Errorf("parsed %d options and %d quests, want none", len(message.Options), len(message.Quests))
	}
}

// TestParseGossipMessage_ShortBuffer keeps a truncated menu from being read as an
// empty one.
func TestParseGossipMessage_ShortBuffer(t *testing.T) {
	_, err := ParseGossipMessage(mustHex(t, `
		01 00 00 00 00 00 00 00
		00 00 00 00
		00 00 00 00
		01 00 00 00
	`))
	if err == nil {
		t.Fatal("parsing a menu that promises an option it does not carry succeeded")
	}
	if !errors.Is(err, ErrShortBuffer) {
		t.Errorf("error = %v, want it to wrap ErrShortBuffer", err)
	}
}
