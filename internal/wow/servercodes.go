package wow

import "fmt"

// QuestStatus mirrors AzerothCore's QuestStatus, the value stored in
// acore_characters.character_queststatus.status and used by the questlog
// packets.
//
// Source: azerothcore-wotlk/src/server/game/Quests/QuestDef.h:98-108. Note the
// gaps at 2 and 4: AC comments them out and they must not be reused.
type QuestStatus uint8

// Quest log states. StatusRewarded is never persisted to the database.
const (
	QuestStatusNone       QuestStatus = 0
	QuestStatusComplete   QuestStatus = 1
	QuestStatusIncomplete QuestStatus = 3
	QuestStatusFailed     QuestStatus = 5
	QuestStatusRewarded   QuestStatus = 6
)

// String returns the AzerothCore enum name, for logs and test failure output.
func (s QuestStatus) String() string {
	switch s {
	case QuestStatusNone:
		return "QUEST_STATUS_NONE"
	case QuestStatusComplete:
		return "QUEST_STATUS_COMPLETE"
	case QuestStatusIncomplete:
		return "QUEST_STATUS_INCOMPLETE"
	case QuestStatusFailed:
		return "QUEST_STATUS_FAILED"
	case QuestStatusRewarded:
		return "QUEST_STATUS_REWARDED"
	default:
		return "QUEST_STATUS_UNKNOWN"
	}
}

// CharCreateResult is the byte SMSG_CHAR_CREATE carries back, i.e. the outcome of
// the character the bot just tried to create.
//
// Source: azerothcore-wotlk/src/server/shared/SharedDefines.h:3622-3678.
// It is the union of two enums, not just CHAR_CREATE_*: the create handler ends
// its name validation with `SendCharCreate(ResponseCodes(res))` where `res` is
// what ObjectMgr::CheckPlayerName returned (CharacterHandler.cpp:355-359), so
// CHAR_NAME_* values (0x57-0x67) arrive in the same field. Digits in a generated
// name therefore come back as CHAR_NAME_MIXED_LANGUAGES (0x5D), not as a
// CHAR_CREATE_* code, and a decoder that only knows the CHAR_CREATE_* range
// reports an unhelpful "unknown" for the one failure our name generator can cause.
type CharCreateResult uint8

// Character creation outcomes.
//
// Only Success means the character exists. NameInUse is the single value worth
// retrying with a different generated name; the rest describe a server-side gate
// (limits, expansion, disabled realm) or a name our generator should never have
// produced, and repeating them with other names would just repeat the failure.
const (
	CharCreateInProgress           CharCreateResult = 0x2E
	CharCreateSuccess              CharCreateResult = 0x2F
	CharCreateError                CharCreateResult = 0x30
	CharCreateFailed               CharCreateResult = 0x31
	CharCreateNameInUse            CharCreateResult = 0x32
	CharCreateDisabled             CharCreateResult = 0x33
	CharCreatePvPTeamsViolation    CharCreateResult = 0x34
	CharCreateServerLimit          CharCreateResult = 0x35
	CharCreateAccountLimit         CharCreateResult = 0x36
	CharCreateServerQueue          CharCreateResult = 0x37
	CharCreateOnlyExisting         CharCreateResult = 0x38
	CharCreateExpansion            CharCreateResult = 0x39
	CharCreateExpansionClass       CharCreateResult = 0x3A
	CharCreateLevelRequirement     CharCreateResult = 0x3B
	CharCreateUniqueClassLimit     CharCreateResult = 0x3C
	CharCreateCharacterInGuild     CharCreateResult = 0x3D
	CharCreateRestrictedRaceClass  CharCreateResult = 0x3E
	CharCreateCharacterChooseRace  CharCreateResult = 0x3F
	CharCreateCharacterArenaLeader CharCreateResult = 0x40
	CharCreateCharacterDeleteMail  CharCreateResult = 0x41
	CharCreateCharacterSwapFaction CharCreateResult = 0x42
	CharCreateCharacterRaceOnly    CharCreateResult = 0x43
	CharCreateCharacterGoldLimit   CharCreateResult = 0x44
	CharCreateForceLogin           CharCreateResult = 0x45

	// Name validation results, reported through the same field as above: these are
	// the values ObjectMgr::CheckPlayerName itself returns. The rules that produce
	// them are implemented in internal/wow/charactername.go.
	CharNameSuccess                       CharCreateResult = 0x57
	CharNameFailure                       CharCreateResult = 0x58
	CharNameNoName                        CharCreateResult = 0x59
	CharNameTooShort                      CharCreateResult = 0x5A
	CharNameTooLong                       CharCreateResult = 0x5B
	CharNameInvalidCharacter              CharCreateResult = 0x5C
	CharNameMixedLanguages                CharCreateResult = 0x5D
	CharNameProfane                       CharCreateResult = 0x5E
	CharNameReserved                      CharCreateResult = 0x5F
	CharNameInvalidApostrophe             CharCreateResult = 0x60
	CharNameMultipleApostrophes           CharCreateResult = 0x61
	CharNameThreeConsecutive              CharCreateResult = 0x62
	CharNameInvalidSpace                  CharCreateResult = 0x63
	CharNameConsecutiveSpaces             CharCreateResult = 0x64
	CharNameRussianConsecutiveSilent      CharCreateResult = 0x65
	CharNameRussianSilentAtBeginOrEnd     CharCreateResult = 0x66
	CharNameDeclensionDoesntMatchBaseName CharCreateResult = 0x67
)

// charCreateResultNames maps wire code to AzerothCore enum name. A map keeps both
// ranges as one list to diff against SharedDefines.h when the server moves.
var charCreateResultNames = map[CharCreateResult]string{
	CharCreateInProgress:                  "CHAR_CREATE_IN_PROGRESS",
	CharCreateSuccess:                     "CHAR_CREATE_SUCCESS",
	CharCreateError:                       "CHAR_CREATE_ERROR",
	CharCreateFailed:                      "CHAR_CREATE_FAILED",
	CharCreateNameInUse:                   "CHAR_CREATE_NAME_IN_USE",
	CharCreateDisabled:                    "CHAR_CREATE_DISABLED",
	CharCreatePvPTeamsViolation:           "CHAR_CREATE_PVP_TEAMS_VIOLATION",
	CharCreateServerLimit:                 "CHAR_CREATE_SERVER_LIMIT",
	CharCreateAccountLimit:                "CHAR_CREATE_ACCOUNT_LIMIT",
	CharCreateServerQueue:                 "CHAR_CREATE_SERVER_QUEUE",
	CharCreateOnlyExisting:                "CHAR_CREATE_ONLY_EXISTING",
	CharCreateExpansion:                   "CHAR_CREATE_EXPANSION",
	CharCreateExpansionClass:              "CHAR_CREATE_EXPANSION_CLASS",
	CharCreateLevelRequirement:            "CHAR_CREATE_LEVEL_REQUIREMENT",
	CharCreateUniqueClassLimit:            "CHAR_CREATE_UNIQUE_CLASS_LIMIT",
	CharCreateCharacterInGuild:            "CHAR_CREATE_CHARACTER_IN_GUILD",
	CharCreateRestrictedRaceClass:         "CHAR_CREATE_RESTRICTED_RACECLASS",
	CharCreateCharacterChooseRace:         "CHAR_CREATE_CHARACTER_CHOOSE_RACE",
	CharCreateCharacterArenaLeader:        "CHAR_CREATE_CHARACTER_ARENA_LEADER",
	CharCreateCharacterDeleteMail:         "CHAR_CREATE_CHARACTER_DELETE_MAIL",
	CharCreateCharacterSwapFaction:        "CHAR_CREATE_CHARACTER_SWAP_FACTION",
	CharCreateCharacterRaceOnly:           "CHAR_CREATE_CHARACTER_RACE_ONLY",
	CharCreateCharacterGoldLimit:          "CHAR_CREATE_CHARACTER_GOLD_LIMIT",
	CharCreateForceLogin:                  "CHAR_CREATE_FORCE_LOGIN",
	CharNameSuccess:                       "CHAR_NAME_SUCCESS",
	CharNameFailure:                       "CHAR_NAME_FAILURE",
	CharNameNoName:                        "CHAR_NAME_NO_NAME",
	CharNameTooShort:                      "CHAR_NAME_TOO_SHORT",
	CharNameTooLong:                       "CHAR_NAME_TOO_LONG",
	CharNameInvalidCharacter:              "CHAR_NAME_INVALID_CHARACTER",
	CharNameMixedLanguages:                "CHAR_NAME_MIXED_LANGUAGES",
	CharNameProfane:                       "CHAR_NAME_PROFANE",
	CharNameReserved:                      "CHAR_NAME_RESERVED",
	CharNameInvalidApostrophe:             "CHAR_NAME_INVALID_APOSTROPHE",
	CharNameMultipleApostrophes:           "CHAR_NAME_MULTIPLE_APOSTROPHES",
	CharNameThreeConsecutive:              "CHAR_NAME_THREE_CONSECUTIVE",
	CharNameInvalidSpace:                  "CHAR_NAME_INVALID_SPACE",
	CharNameConsecutiveSpaces:             "CHAR_NAME_CONSECUTIVE_SPACES",
	CharNameRussianConsecutiveSilent:      "CHAR_NAME_RUSSIAN_CONSECUTIVE_SILENT_CHARACTERS",
	CharNameRussianSilentAtBeginOrEnd:     "CHAR_NAME_RUSSIAN_SILENT_CHARACTER_AT_BEGINNING_OR_END",
	CharNameDeclensionDoesntMatchBaseName: "CHAR_NAME_DECLENSION_DOESNT_MATCH_BASE_NAME",
}

// String returns the AzerothCore enum name, for logs. Unknown codes still print
// the numeric value, so a log line alone is enough to look the code up.
func (c CharCreateResult) String() string {
	if name, ok := charCreateResultNames[c]; ok {
		return name
	}
	return fmt.Sprintf("CHAR_CREATE_UNKNOWN(0x%02X)", uint8(c))
}
