package wow

import "strings"

// LfgRole is the role bitmask carried in CMSG_LFG_JOIN.Roles and
// CMSG_LFG_SET_ROLES.
//
// Source: azerothcore-wotlk/src/server/game/DungeonFinding/LFG.h:35-42
// (LfgRoles). The client picks exactly one of Tank/Healer/Damage; Leader is set
// by the server in related SMSG packets and is never sent by the client.
type LfgRole uint8

// LFG role bits, as defined by the LfgRoles enum.
const (
	LfgRoleNone   LfgRole = 0x00
	LfgRoleLeader LfgRole = 0x01
	LfgRoleTank   LfgRole = 0x02
	LfgRoleHealer LfgRole = 0x04
	LfgRoleDamage LfgRole = 0x08
)

// String renders the role bits for logs, e.g. "tank|healer". It returns "none"
// for an empty mask and "unknown" when only undefined bits are set.
func (r LfgRole) String() string {
	if r == LfgRoleNone {
		return "none"
	}
	names := make([]string, 0, 4)
	if r&LfgRoleLeader != 0 {
		names = append(names, "leader")
	}
	if r&LfgRoleTank != 0 {
		names = append(names, "tank")
	}
	if r&LfgRoleHealer != 0 {
		names = append(names, "healer")
	}
	if r&LfgRoleDamage != 0 {
		names = append(names, "damage")
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, "|")
}

// LFG dungeon categories, stored in the high byte of a CMSG_LFG_JOIN slot.
//
// Source: azerothcore-wotlk/src/server/game/DungeonFinding/LFG.h (LfgType).
const (
	LfgTypeNone           uint32 = 0
	LfgTypeDungeon        uint32 = 1
	LfgTypeRaid           uint32 = 2
	LfgTypeQuest          uint32 = 3
	LfgTypeZone           uint32 = 4
	LfgTypeHeroic         uint32 = 5
	LfgTypeRandom         uint32 = 6
	LfgTypeRandomRaid     uint32 = 7
	LfgTypeSeasonal       uint32 = 8
	LfgTypeRandomSeasonal uint32 = 9
)

// LfgSlot packs a dungeon id and its category into a CMSG_LFG_JOIN slot entry.
//
// AC: LFGHandler.cpp:64-69 — the server keeps `slot & 0x00FFFFFF` as the dungeon
// id and ignores entries whose id is not present in the lfg_dungeon_template
// table. If every slot is dropped that way the resulting set is empty and the
// join is silently ignored, so the id must be a real dungeon id from that table.
func LfgSlot(dungeonID, dungeonType uint32) uint32 {
	return (dungeonType << 24) | (dungeonID & 0x00FFFFFF)
}

// LfgDungeonID extracts the dungeon id back out of a packed slot.
func LfgDungeonID(slot uint32) uint32 { return slot & 0x00FFFFFF }

// LfgDungeonType extracts the category back out of a packed slot.
func LfgDungeonType(slot uint32) uint32 { return slot >> 24 }

// LfgDefaultNeeds is the role requirement triple the 3.3.5a client always
// sends: one tank, one healer, three damage dealers.
//
// Source: LFG.h:27-31 (LFGEnum: LFG_TANKS_NEEDED = 1, LFG_HEALERS_NEEDED = 1,
// LFG_DPS_NEEDED = 3). The server discards these values, but the field must be
// present for the packet to be parsed at the correct offsets.
var LfgDefaultNeeds = [3]uint8{1, 1, 3}

// LfgDeserterSpell is the "Dungeon Deserter" debuff applied when a player
// abandons a dungeon group. While it is active the server refuses new dungeon
// finder queues.
//
// Source: LFGMgr.h:54 (LFG_SPELL_DUNGEON_DESERTER = 71041).
const LfgDeserterSpell uint32 = 71041

// LFG option bits of the server's DungeonFinder.OptionsMask setting.
//
// Source: LFGMgr.h:42-44 (LFGOptions). The AC default is 5, i.e. dungeon finder
// plus seasonal bosses. If the bit for the content a bot queues for is not set,
// CMSG_LFG_JOIN is dropped without a reply (LFGHandler.cpp:52-55), which looks
// exactly like an unresponsive server.
const (
	LfgOptionEnableDungeonFinder uint32 = 0x01
	LfgOptionEnableRaidBrowser   uint32 = 0x02
	LfgOptionEnableSeasonalBoss  uint32 = 0x04
)
