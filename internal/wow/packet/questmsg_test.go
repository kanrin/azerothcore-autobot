package packet

import (
	"errors"
	"strings"
	"testing"
)

// Golden bytes for the quest SMSG parsers, transcribed by hand from the
// AzerothCore code that writes those packets (line numbers in questmsg.go).
//
// None of these expectations may be produced by our own writer: a test built from
// our own encoder only proves the encoder agrees with itself, while the whole point
// here is that our field order matches the server's.
func TestParseQuestGiverQuestList(t *testing.T) {
	// AC GossipDef.cpp:317-373:
	//   guid, greeting, uint32 emoteDelay, uint32 emoteType, uint8 count,
	//   count × { uint32 id, uint32 icon, int32 level, uint32 flags,
	//             uint8 repeatable, title }
	//
	// The second entry pins two things a naive parser gets wrong: the level is
	// *signed* (-1 is legal) and the flags are a plain uint32 (0x200 is
	// QUEST_FLAGS_HIDDEN_REWARDS, QuestDef.h:141).
	payload := mustHex(t, `
		BC 0A 00 00 00 00 00 00
		48 69 00
		64 00 00 00
		01 00 00 00
		02
		0F 03 00 00
		02 00 00 00
		01 00 00 00
		00 00 00 00
		00
		41 20 54 68 72 65 61 74 20 57 69 74 68 69 6E 00
		8D 14 00 00
		00 00 00 00
		FF FF FF FF
		00 02 00 00
		01
		45 61 67 61 6E 00
	`)

	list, err := ParseQuestGiverQuestList(payload)
	if err != nil {
		t.Fatalf("ParseQuestGiverQuestList: %v", err)
	}
	if list.GiverGUID != 0xABC {
		t.Errorf("GiverGUID = %#x, want 0xABC", list.GiverGUID)
	}
	if list.Greeting != "Hi" {
		t.Errorf("Greeting = %q, want %q", list.Greeting, "Hi")
	}
	if list.EmoteDelay != 100 || list.EmoteType != 1 {
		t.Errorf("emotes = %d/%d, want 100/1", list.EmoteDelay, list.EmoteType)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(list.Entries))
	}

	want := QuestListEntry{ID: 783, Icon: 2, Level: 1, Title: "A Threat Within"}
	if list.Entries[0] != want {
		t.Errorf("entry 1 = %+v, want %+v", list.Entries[0], want)
	}

	want = QuestListEntry{ID: 5261, Level: -1, Flags: 0x200, Repeatable: true, Title: "Eagan"}
	if list.Entries[1] != want {
		t.Errorf("entry 2 = %+v, want %+v", list.Entries[1], want)
	}
}

func TestParseQuestGiverQuestDetails(t *testing.T) {
	// AC GossipDef.cpp:405-413: npc guid, divider guid, quest id, title, details,
	// objectives, uint8 activateAccept, uint32 flags, uint32 suggestedPlayers.
	//
	// The trailing bytes belong to the real packet — they are the reward block
	// (GossipDef.cpp:414+) — and must be ignored: a parser that only reads the
	// prefix cannot misread what it does not read.
	payload := mustHex(t, `
		BC 0A 00 00 00 00 00 00
		00 00 00 00 00 00 00 00
		0F 03 00 00
		41 20 54 68 72 65 61 74 20 57 69 74 68 69 6E 00
		53 70 65 61 6B 20 74 6F 20 4D 63 42 72 69 64 65 2E 00
		00
		01
		00 00 00 00
		00 00 00 00
		DE AD BE EF
		01 00 00 00
	`)

	details, err := ParseQuestGiverQuestDetails(payload)
	if err != nil {
		t.Fatalf("ParseQuestGiverQuestDetails: %v", err)
	}
	if details.GiverGUID != 0xABC || details.DividerGUID != 0 {
		t.Errorf("guids = %#x/%#x, want 0xABC/0", details.GiverGUID, details.DividerGUID)
	}
	if details.QuestID != 783 {
		t.Errorf("QuestID = %d, want 783", details.QuestID)
	}
	if details.Title != "A Threat Within" {
		t.Errorf("Title = %q", details.Title)
	}
	if details.Details != "Speak to McBride." {
		t.Errorf("Details = %q", details.Details)
	}
	if details.Objectives != "" {
		t.Errorf("Objectives = %q, want empty: a delivery quest has no objectives text", details.Objectives)
	}
	if !details.AutoAccept {
		t.Error("AutoAccept = false, want true: the byte was 1")
	}
	if details.Flags != 0 || details.SuggestedPlayers != 0 {
		t.Errorf("flags/suggested = %d/%d, want 0/0", details.Flags, details.SuggestedPlayers)
	}
}

func TestParseQuestGiverQuestInvalid(t *testing.T) {
	// AC QuestPackets.cpp:54-59 writes the reason alone. QuestDef.h:49 makes
	// 1 = INVALIDREASON_QUEST_FAILED_LOW_LEVEL, the refusal a level-1 bot is most
	// likely to meet.
	invalid, err := ParseQuestGiverQuestInvalid(mustHex(t, "01 00 00 00"))
	if err != nil {
		t.Fatalf("ParseQuestGiverQuestInvalid: %v", err)
	}
	if invalid.Reason != 1 {
		t.Errorf("Reason = %d, want 1 (low level)", invalid.Reason)
	}
}

func TestParseQuestUpdateAddKill(t *testing.T) {
	// AC QuestPackets.cpp:78-88: quest id, creature entry, current, required,
	// objective guid as a raw uint64.
	kill, err := ParseQuestUpdateAddKill(mustHex(t, `
		12 00 00 00
		2B 01 00 00
		03 00 00 00
		08 00 00 00
		88 77 66 55 44 33 22 11
	`))
	if err != nil {
		t.Fatalf("ParseQuestUpdateAddKill: %v", err)
	}
	want := QuestAddKill{QuestID: 18, CreatureEntry: 299, Current: 3, Required: 8,
		ObjectiveGUID: 0x1122334455667788}
	if kill != want {
		t.Errorf("kill = %+v, want %+v", kill, want)
	}
}

func TestParseQuestUpdate(t *testing.T) {
	// AC QuestPackets.cpp:20-52: a single uint32 quest id, for complete, failed
	// and failedtimer alike.
	update, err := ParseQuestUpdate(mustHex(t, "0F 03 00 00"))
	if err != nil {
		t.Fatalf("ParseQuestUpdate: %v", err)
	}
	if update.QuestID != 783 {
		t.Errorf("QuestID = %d, want 783", update.QuestID)
	}
}

// TestParseQuestMessages_ShortBuffer keeps a truncated packet from being read as a
// zero-filled success: a parser that swallowed the error would report "quest 0" and
// send the caller looking for a quest that does not exist.
func TestParseQuestMessages_ShortBuffer(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
		call    func([]byte) error
	}{
		{
			name: "an empty quest list",
			call: func(b []byte) error { _, err := ParseQuestGiverQuestList(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_LIST",
		},
		{
			// The header is complete and the count claims two entries, but only
			// one is present: the tail must be an error, not a short entry.
			name: "a quest list that promises more entries than it carries",
			payload: `
				BC 0A 00 00 00 00 00 00
				48 69 00
				00 00 00 00
				00 00 00 00
				02
				0F 03 00 00
			`,
			call: func(b []byte) error { _, err := ParseQuestGiverQuestList(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_LIST",
		},
		{
			name: "details cut off right after the guids",
			payload: `
				BC 0A 00 00 00 00 00 00
				00 00 00 00 00 00 00 00
			`,
			call: func(b []byte) error { _, err := ParseQuestGiverQuestDetails(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_DETAILS",
		},
		{
			name: "a details packet whose title has no terminator",
			payload: `
				BC 0A 00 00 00 00 00 00
				00 00 00 00 00 00 00 00
				0F 03 00 00
				41 20 54
			`,
			call: func(b []byte) error { _, err := ParseQuestGiverQuestDetails(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_DETAILS",
		},
		{
			name: "an invalid-reason packet with no reason",
			call: func(b []byte) error { _, err := ParseQuestGiverQuestInvalid(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_INVALID",
		},
		{
			name: "add-kill cut off inside the objective guid",
			payload: `
				12 00 00 00
				2B 01 00 00
				03 00 00 00
				08 00 00 00
				88 77 66
			`,
			call: func(b []byte) error { _, err := ParseQuestUpdateAddKill(b); return err },
			want: "SMSG_QUESTUPDATE_ADD_KILL",
		},
		{
			name: "an update with no quest id",
			call: func(b []byte) error { _, err := ParseQuestUpdate(b); return err },
			want: "SMSG_QUESTUPDATE_",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(mustHex(t, tc.payload))
			if err == nil {
				t.Fatal("parsing a truncated payload succeeded, want an error")
			}
			if !errors.Is(err, ErrShortBuffer) && !errors.Is(err, ErrUnterminated) {
				t.Errorf("error = %v, want it to wrap ErrShortBuffer or ErrUnterminated", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %s", err, tc.want)
			}
		})
	}
}
