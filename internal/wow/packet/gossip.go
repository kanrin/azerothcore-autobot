package packet

// Gossip client packet encoders.
//
// AzerothGhost v1.0.9 implements none of these. Layouts come from the
// AzerothCore handlers registered in
// src/server/game/Server/Protocol/Opcodes.cpp.

// EncodeGossipHello encodes CMSG_GOSSIP_HELLO (0x17B).
//
// AC: NPCHandler.cpp:139-142, `recvData >> guid`; registered at
// Opcodes.cpp:510. The server only answers when the creature actually has NPC
// flags, so a silent failure here usually means the wrong NPC.
func EncodeGossipHello(npcGUID uint64) []byte {
	return NewWriter().RawGUID(npcGUID).Bytes()
}

// EncodeGossipSelectOption encodes CMSG_GOSSIP_SELECT_OPTION (0x17C).
//
// AC: MiscHandler.cpp:89-107, registered at Opcodes.cpp:511:
//
//	recv_data >> guid >> menuId >> gossipListId;
//	if (_player->PlayerTalkClass->IsGossipOptionCoded(gossipListId))
//	    recv_data >> code;
//
// The trailing code is only present for coded options; pass an empty code for
// the normal case and no bytes will be appended. The server also drops the
// packet unless the remembered gossip sender guid and menuId match, so gossip
// must be opened with EncodeGossipHello immediately beforehand.
func EncodeGossipSelectOption(guid uint64, menuID, gossipListID uint32, code string) []byte {
	w := NewWriter().RawGUID(guid).Uint32(menuID).Uint32(gossipListID)
	if code != "" {
		w.CString(code)
	}
	return w.Bytes()
}

// EncodeNpcTextQuery encodes CMSG_NPC_TEXT_QUERY (0x17F).
//
// AC: QueryHandler.cpp:274-282, registered at Opcodes.cpp:514 —
// `recvData >> textID >> guid` (uint32 then raw uint64).
func EncodeNpcTextQuery(textID uint32, guid uint64) []byte {
	return NewWriter().Uint32(textID).RawGUID(guid).Bytes()
}

// EncodeQuestgiverStatusMultipleQuery encodes
// CMSG_QUESTGIVER_STATUS_MULTIPLE_QUERY (0x417).
//
// AC: QuestHandler.cpp:620-623, registered at Opcodes.cpp:1178. The handler
// signature takes the packet but reads nothing at all — it answers for every
// questgiver currently in visibility. An empty payload is correct.
func EncodeQuestgiverStatusMultipleQuery() []byte {
	return NewWriter().Bytes()
}
