package packet

import "testing"

// Golden bytes for the quest CMSG encoders, transcribed from the AzerothCore
// handlers cited in quest.go. GUID 0xABC is deliberately small: a packed guid
// would fit in two bytes, so these expectations also prove the raw encoding.
func TestEncodeQuest(t *testing.T) {
	const (
		npcGUID = 0x0000000000000ABC
		questID = 0x000004D2
	)

	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{
			// AC QuestHandler.cpp:243 — `recvData >> questId`.
			name: "QuestQuery",
			got:  EncodeQuestQuery(questID),
			want: "D2 04 00 00",
		},
		{
			// AC QuestHandler.cpp:82 — `recvData >> guid`.
			name: "QuestgiverHello",
			got:  EncodeQuestgiverHello(npcGUID),
			want: "BC 0A 00 00 00 00 00 00",
		},
		{
			// AC QuestHandler.cpp:208 — guid, questId, uint8 unk.
			name: "QuestgiverQueryQuest",
			got:  EncodeQuestgiverQueryQuest(npcGUID, questID, 0),
			want: "BC 0A 00 00 00 00 00 00" +
				" D2 04 00 00" +
				" 00",
		},
		{
			// AC QuestHandler.cpp:118 — guid, questId, uint32 unk (client sends 0).
			name: "QuestgiverAcceptQuest",
			got:  EncodeQuestgiverAcceptQuest(npcGUID, questID),
			want: "BC 0A 00 00 00 00 00 00" +
				" D2 04 00 00" +
				" 00 00 00 00",
		},
		{
			// AC QuestHandler.cpp:483 — guid, questId.
			name: "QuestgiverCompleteQuest",
			got:  EncodeQuestgiverCompleteQuest(npcGUID, questID),
			want: "BC 0A 00 00 00 00 00 00" +
				" D2 04 00 00",
		},
		{
			// AC QuestHandler.cpp:255 — guid, questId, reward.
			name: "QuestgiverChooseReward",
			got:  EncodeQuestgiverChooseReward(npcGUID, questID, 2),
			want: "BC 0A 00 00 00 00 00 00" +
				" D2 04 00 00" +
				" 02 00 00 00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertBytes(t, tc.want, tc.got)
		})
	}
}

// TestEncodeQuestGUIDIsRawNotPacked guards the easiest encoding mistake in this
// file: quest packets carry a raw little-endian uint64 guid
// (ObjectGuid.cpp:76), so even a tiny npc guid occupies eight bytes. The packed
// form of 0xABC would be two bytes, 0x03 0xBC.
func TestEncodeQuestGUIDIsRawNotPacked(t *testing.T) {
	got := EncodeQuestgiverHello(0x0000000000000ABC)
	if len(got) != 8 {
		t.Fatalf("QuestgiverHello payload = %d bytes, want 8 (raw uint64 guid): % x", len(got), got)
	}
	assertBytes(t, "BC 0A 00 00 00 00 00 00", got)
}

// TestEncodeQuestBoundaryValues keeps the field widths honest at both extremes.
func TestEncodeQuestBoundaryValues(t *testing.T) {
	t.Run("all bits set", func(t *testing.T) {
		assertBytes(t, "FF FF FF FF FF FF FF FF"+
			" FF FF FF FF"+
			" 00",
			EncodeQuestgiverQueryQuest(0xFFFFFFFFFFFFFFFF, 0xFFFFFFFF, 0))
	})
	t.Run("all zero", func(t *testing.T) {
		assertBytes(t, "00 00 00 00 00 00 00 00"+
			" 00 00 00 00",
			EncodeQuestgiverCompleteQuest(0, 0))
	})
	t.Run("quest id is unsigned", func(t *testing.T) {
		assertBytes(t, "00 00 00 00 00 00 00 00"+
			" FF FF FF FF",
			EncodeQuestgiverCompleteQuest(0, 0xFFFFFFFF))
	})
}
