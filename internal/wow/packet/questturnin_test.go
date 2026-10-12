package packet

import (
	"errors"
	"strings"
	"testing"
)

// Golden bytes for the turn-in parsers, transcribed from the writers cited in
// questturnin.go.
func TestParseQuestGiverRequestItems(t *testing.T) {
	// GossipDef.cpp:791-796: guid, quest id, title, request text, then an unknown
	// uint32 which the parser is allowed to ignore.
	items, err := ParseQuestGiverRequestItems(mustHex(t, `
		BC 0A 00 00 00 00 00 00
		12 00 00 00
		42 72 6F 74 68 65 72 68 6F 6F 64 20 6F 66 20 54 68 69 65 76 65 73 00
		42 72 69 6E 67 20 6D 65 20 74 68 65 20 62 61 6E 64 61 6E 61 73 2E 00
		00 00 00 00
		DE AD BE EF
	`))
	if err != nil {
		t.Fatalf("ParseQuestGiverRequestItems: %v", err)
	}
	if items.GiverGUID != 0xABC || items.QuestID != 18 {
		t.Errorf("guid/quest = %#x/%d, want 0xABC/18", items.GiverGUID, items.QuestID)
	}
	if items.Title != "Brotherhood of Thieves" {
		t.Errorf("Title = %q", items.Title)
	}
	if items.RequestText != "Bring me the bandanas." {
		t.Errorf("RequestText = %q", items.RequestText)
	}
}

func TestParseQuestGiverOfferReward(t *testing.T) {
	// GossipDef.cpp:659-692: guid, quest id, title, reward text, auto-finish byte,
	// flags, suggested players, emote count + emotes, choice count + choices.
	offer, err := ParseQuestGiverOfferReward(mustHex(t, `
		BC 0A 00 00 00 00 00 00
		12 00 00 00
		42 72 6F 74 68 65 72 68 6F 6F 64 20 6F 66 20 54 68 69 65 76 65 73 00
		57 65 6C 6C 20 64 6F 6E 65 2E 00
		01
		00 00 00 00
		00 00 00 00
		01 00 00 00
		64 00 00 00
		01 00 00 00
		02 00 00 00
		0A 00 00 00
		50 00 00 00
		02 00 00 00
		0F 00 00 00
		00 00 00 00
		00 00 00 00
	`))
	if err != nil {
		t.Fatalf("ParseQuestGiverOfferReward: %v", err)
	}
	if offer.GiverGUID != 0xABC || offer.QuestID != 18 {
		t.Errorf("guid/quest = %#x/%d, want 0xABC/18", offer.GiverGUID, offer.QuestID)
	}
	if offer.Title != "Brotherhood of Thieves" || offer.RewardText != "Well done." {
		t.Errorf("title/reward text = %q/%q", offer.Title, offer.RewardText)
	}
	if !offer.AutoFinish {
		t.Error("AutoFinish = false, want true: the byte was 1")
	}
	if len(offer.Choices) != 2 {
		t.Fatalf("choices = %d, want 2", len(offer.Choices))
	}
	want := []QuestRewardItem{
		{ItemID: 10, Count: 80, DisplayID: 2},
		{ItemID: 15, Count: 0, DisplayID: 0},
	}
	for i, choice := range offer.Choices {
		if choice != want[i] {
			t.Errorf("choice %d = %+v, want %+v", i, choice, want[i])
		}
	}
}

func TestParseQuestGiverQuestComplete(t *testing.T) {
	// QuestPackets.cpp:27-37 writes six uint32s.
	reward, err := ParseQuestGiverQuestComplete(mustHex(t, `
		12 00 00 00
		E8 03 00 00
		64 00 00 00
		00 00 00 00
		01 00 00 00
		00 00 00 00
	`))
	if err != nil {
		t.Fatalf("ParseQuestGiverQuestComplete: %v", err)
	}
	want := QuestReward{QuestID: 18, Experience: 1000, Money: 100, TalentPoints: 1}
	if reward != want {
		t.Errorf("reward = %+v, want %+v", reward, want)
	}
}

// TestParseQuestTurnIn_ShortBuffer keeps a truncated dialog from being read as an
// empty one, which would look like "the quest is ready with no rewards".
func TestParseQuestTurnIn_ShortBuffer(t *testing.T) {
	tests := []struct {
		name string
		want string
		call func([]byte) error
	}{
		{
			name: "request items with no text",
			call: func(b []byte) error { _, err := ParseQuestGiverRequestItems(b); return err },
			want: "SMSG_QUESTGIVER_REQUEST_ITEMS",
		},
		{
			name: "offer reward cut off inside the choice block",
			call: func(b []byte) error { _, err := ParseQuestGiverOfferReward(b); return err },
			want: "SMSG_QUESTGIVER_OFFER_REWARD",
		},
		{
			name: "complete with no quest id",
			call: func(b []byte) error { _, err := ParseQuestGiverQuestComplete(b); return err },
			want: "SMSG_QUESTGIVER_QUEST_COMPLETE",
		},
	}

	payloads := map[string]string{
		"request items with no text": `
			BC 0A 00 00 00 00 00 00
			12 00 00 00
		`,
		"offer reward cut off inside the choice block": `
			BC 0A 00 00 00 00 00 00
			12 00 00 00
			54 00
			57 00
			01
			00 00 00 00
			00 00 00 00
			00 00 00 00
			02 00 00 00
			0A 00 00 00
			50 00 00 00
		`,
		"complete with no quest id": ``,
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(mustHex(t, payloads[tc.name]))
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
