package wow

import "testing"

// TestLfgRoleString checks the log rendering of the role bitmask.
func TestLfgRoleString(t *testing.T) {
	tests := []struct {
		role LfgRole
		want string
	}{
		{LfgRoleNone, "none"},
		{LfgRoleLeader, "leader"},
		{LfgRoleTank, "tank"},
		{LfgRoleHealer, "healer"},
		{LfgRoleDamage, "damage"},
		{LfgRoleTank | LfgRoleHealer, "tank|healer"},
		{LfgRoleTank | LfgRoleHealer | LfgRoleDamage, "tank|healer|damage"},
		{LfgRole(0x80), "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.role.String(); got != tc.want {
				t.Fatalf("LfgRole(%#x).String() = %q, want %q", uint8(tc.role), got, tc.want)
			}
		})
	}
}

// TestLfgSlot checks the slot packing the server expects: dungeon id in the low
// 24 bits, category in the high byte (LFGHandler.cpp:66 masks off the high
// byte).
func TestLfgSlot(t *testing.T) {
	tests := []struct {
		name        string
		dungeonID   uint32
		dungeonType uint32
		want        uint32
	}{
		{"random dungeon id 1", 1, LfgTypeRandom, 0x06000001},
		{"normal dungeon id 0x22", 0x22, LfgTypeDungeon, 0x01000022},
		{"heroic dungeon id 0x24", 0x24, LfgTypeHeroic, 0x05000024},
		{"seasonal id 0x1F", 0x1F, LfgTypeSeasonal, 0x0800001F},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LfgSlot(tc.dungeonID, tc.dungeonType)
			if got != tc.want {
				t.Fatalf("LfgSlot(%d, %d) = %#08x, want %#08x", tc.dungeonID, tc.dungeonType, got, tc.want)
			}
			if back := LfgDungeonID(got); back != tc.dungeonID {
				t.Errorf("LfgDungeonID round trip = %d, want %d", back, tc.dungeonID)
			}
			if back := LfgDungeonType(got); back != tc.dungeonType {
				t.Errorf("LfgDungeonType round trip = %d, want %d", back, tc.dungeonType)
			}
		})
	}
}

// TestLfgSlotMasksHighBitsOfDungeonID: the id field is 24 bits wide, so a
// dungeon id with bits above 0xFFFFFF must not bleed into the category byte.
func TestLfgSlotMasksHighBitsOfDungeonID(t *testing.T) {
	got := LfgSlot(0xFF123456, LfgTypeRandom)
	if got != 0x06123456 {
		t.Fatalf("LfgSlot = %#08x, want 0x06123456", got)
	}
}

// TestLfgDefaultNeeds: the 3.3.5a client always announces one tank, one healer
// and three damage dealers (LFG.h:27-31).
func TestLfgDefaultNeeds(t *testing.T) {
	want := [3]uint8{1, 1, 3}
	if LfgDefaultNeeds != want {
		t.Fatalf("LfgDefaultNeeds = %v, want %v", LfgDefaultNeeds, want)
	}
}

// TestLfgDeserterSpell pins the debuff that blocks requeueing.
func TestLfgDeserterSpell(t *testing.T) {
	if LfgDeserterSpell != 71041 {
		t.Fatalf("LfgDeserterSpell = %d, want 71041 (LFGMgr.h:54)", LfgDeserterSpell)
	}
}

// TestLfgOptionMask: the AC default DungeonFinder.OptionsMask is 5, i.e. the
// dungeon finder plus seasonal bosses, which is exactly the combination this
// project relies on.
func TestLfgOptionMask(t *testing.T) {
	const acDefault = LfgOptionEnableDungeonFinder | LfgOptionEnableSeasonalBoss
	if acDefault != 5 {
		t.Fatalf("dungeon finder | seasonal bosses = %d, want 5", acDefault)
	}
	if acDefault&LfgOptionEnableDungeonFinder == 0 {
		t.Fatal("the default mask does not enable the dungeon finder")
	}
}

// TestQuestStatusValues records the enum as AzerothCore defines it. The gaps at
// 2 and 4 are commented out upstream and must not be reused.
func TestQuestStatusValues(t *testing.T) {
	tests := []struct {
		status QuestStatus
		want   uint8
		name   string
	}{
		{QuestStatusNone, 0, "QUEST_STATUS_NONE"},
		{QuestStatusComplete, 1, "QUEST_STATUS_COMPLETE"},
		{QuestStatusIncomplete, 3, "QUEST_STATUS_INCOMPLETE"},
		{QuestStatusFailed, 5, "QUEST_STATUS_FAILED"},
		{QuestStatusRewarded, 6, "QUEST_STATUS_REWARDED"},
	}

	seen := map[uint8]bool{}
	for _, tc := range tests {
		if uint8(tc.status) != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, uint8(tc.status), tc.want)
		}
		if tc.status.String() != tc.name {
			t.Errorf("QuestStatus(%d).String() = %q, want %q", tc.want, tc.status.String(), tc.name)
		}
		if seen[tc.want] {
			t.Errorf("duplicate value %d", tc.want)
		}
		seen[tc.want] = true
	}
}

// TestCharCreateResultValues pins the creation codes the login flow branches
// on: success means the character exists, name-in-use means retry with a new
// name, and everything else is a hard failure. The CHAR_NAME_* entries are there
// because AC forwards CheckPlayerName's result through the same field
// (CharacterHandler.cpp:355-359), so they are the codes our name generator can
// actually provoke.
func TestCharCreateResultValues(t *testing.T) {
	tests := []struct {
		result CharCreateResult
		want   uint8
		name   string
	}{
		{CharCreateInProgress, 0x2E, "CHAR_CREATE_IN_PROGRESS"},
		{CharCreateSuccess, 0x2F, "CHAR_CREATE_SUCCESS"},
		{CharCreateError, 0x30, "CHAR_CREATE_ERROR"},
		{CharCreateFailed, 0x31, "CHAR_CREATE_FAILED"},
		{CharCreateNameInUse, 0x32, "CHAR_CREATE_NAME_IN_USE"},
		{CharCreateDisabled, 0x33, "CHAR_CREATE_DISABLED"},
		{CharCreatePvPTeamsViolation, 0x34, "CHAR_CREATE_PVP_TEAMS_VIOLATION"},
		{CharCreateServerLimit, 0x35, "CHAR_CREATE_SERVER_LIMIT"},
		{CharCreateAccountLimit, 0x36, "CHAR_CREATE_ACCOUNT_LIMIT"},
		{CharCreateExpansion, 0x39, "CHAR_CREATE_EXPANSION"},
		{CharCreateExpansionClass, 0x3A, "CHAR_CREATE_EXPANSION_CLASS"},
		{CharCreateLevelRequirement, 0x3B, "CHAR_CREATE_LEVEL_REQUIREMENT"},
		{CharCreateUniqueClassLimit, 0x3C, "CHAR_CREATE_UNIQUE_CLASS_LIMIT"},
		{CharNameSuccess, 0x57, "CHAR_NAME_SUCCESS"},
		{CharNameTooShort, 0x5A, "CHAR_NAME_TOO_SHORT"},
		{CharNameTooLong, 0x5B, "CHAR_NAME_TOO_LONG"},
		{CharNameMixedLanguages, 0x5D, "CHAR_NAME_MIXED_LANGUAGES"},
		{CharNameProfane, 0x5E, "CHAR_NAME_PROFANE"},
		{CharNameReserved, 0x5F, "CHAR_NAME_RESERVED"},
		{CharNameThreeConsecutive, 0x62, "CHAR_NAME_THREE_CONSECUTIVE"},
	}

	seen := map[uint8]bool{}
	for _, tc := range tests {
		if uint8(tc.result) != tc.want {
			t.Errorf("%s = %#x, want %#x", tc.name, uint8(tc.result), tc.want)
		}
		if tc.result.String() != tc.name {
			t.Errorf("CharCreateResult(%#x).String() = %q, want %q", tc.want, tc.result.String(), tc.name)
		}
		if seen[tc.want] {
			t.Errorf("duplicate code %#x", tc.want)
		}
		seen[tc.want] = true
	}

	if CharCreateSuccess != 0x2F || CharCreateNameInUse != 0x32 {
		t.Fatalf("success/name-in-use codes drifted from AC SharedDefines.h:3623-3626")
	}
	if CharNameMixedLanguages != 0x5D || CharNameThreeConsecutive != 0x62 {
		t.Fatalf("name codes drifted from AC SharedDefines.h:3658-3673")
	}
}

// TestCharCreateResultUnknownKeepsTheCode: an unknown code must stay identifiable
// from the log line alone, otherwise triaging a failed create needs a packet
// capture. AzerothCore defines 0x46-0x56 as CHAR_DELETE_*/CHAR_LOGIN_* (sent in
// other packets), so a stray one here is a real anomaly worth seeing the byte of.
func TestCharCreateResultUnknownKeepsTheCode(t *testing.T) {
	got := CharCreateResult(0x4D).String()
	if got != "CHAR_CREATE_UNKNOWN(0x4D)" {
		t.Fatalf("CharCreateResult(0x4D).String() = %q, want %q", got, "CHAR_CREATE_UNKNOWN(0x4D)")
	}
}
