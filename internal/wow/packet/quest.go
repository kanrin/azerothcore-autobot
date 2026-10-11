package packet

// Quest client packet encoders.
//
// Layouts are copied from the AzerothCore handlers that read them
// (src/server/game/Handlers/QuestHandler.cpp). Every GUID below is read with
// `>> guid`, i.e. the raw uint64 form.
//
// AzerothGhost v1.0.9 already sends four of these (client/world.go:3055-3085).
// They are repeated here so the wire format stays covered by our golden tests
// and so the two missing ones can live beside them.

// EncodeQuestQuery encodes CMSG_QUEST_QUERY (0x05C).
//
// AC: QuestHandler.cpp:237-243, HandleQuestQueryOpcode reads a single uint32
// quest id. Not implemented by AzerothGhost.
func EncodeQuestQuery(questID uint32) []byte {
	return NewWriter().Uint32(questID).Bytes()
}

// EncodeQuestgiverHello encodes CMSG_QUESTGIVER_HELLO (0x184).
//
// AC: QuestHandler.cpp:79-82, `recvData >> guid`.
func EncodeQuestgiverHello(npcGUID uint64) []byte {
	return NewWriter().RawGUID(npcGUID).Bytes()
}

// EncodeQuestgiverQueryQuest encodes CMSG_QUESTGIVER_QUERY_QUEST (0x186).
//
// AC: QuestHandler.cpp:203-208, `recvData >> guid >> questId >> unk1` where
// unk1 is a uint8. The client sends 0. Not implemented by AzerothGhost.
//
// The server requires the guid to be a questgiver that offers, or is otherwise
// involved in, questID; otherwise the packet is ignored without a reply.
func EncodeQuestgiverQueryQuest(npcGUID uint64, questID uint32, unk1 uint8) []byte {
	return NewWriter().RawGUID(npcGUID).Uint32(questID).Uint8(unk1).Bytes()
}

// EncodeQuestgiverAcceptQuest encodes CMSG_QUESTGIVER_ACCEPT_QUEST (0x189).
//
// AC: QuestHandler.cpp:113-118, `recvData >> guid >> questId >> unk1` where
// unk1 is a uint32. The 3.3.5a client sends 0, matching AzerothGhost
// (client/world.go:3062-3068).
func EncodeQuestgiverAcceptQuest(npcGUID uint64, questID uint32) []byte {
	return NewWriter().RawGUID(npcGUID).Uint32(questID).Uint32(0).Bytes()
}

// EncodeQuestgiverCompleteQuest encodes CMSG_QUESTGIVER_COMPLETE_QUEST (0x18A).
//
// AC: QuestHandler.cpp:478-483, `recvData >> guid >> questId`.
//
// Note the handler returns silently unless the target object hasInvolvedQuest
// (questID), so querying/accepting first is a real prerequisite, not a
// formality.
func EncodeQuestgiverCompleteQuest(npcGUID uint64, questID uint32) []byte {
	return NewWriter().RawGUID(npcGUID).Uint32(questID).Bytes()
}

// EncodeQuestgiverChooseReward encodes CMSG_QUESTGIVER_CHOOSE_REWARD (0x18E).
//
// AC: QuestHandler.cpp:250-255, `recvData >> guid >> questId >> reward`.
// The handler rejects reward >= QUEST_REWARD_CHOICES_COUNT (6).
func EncodeQuestgiverChooseReward(npcGUID uint64, questID, reward uint32) []byte {
	return NewWriter().RawGUID(npcGUID).Uint32(questID).Uint32(reward).Bytes()
}
