package wow

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/azerothcore/AzerothGhost/client"
)

// Character-select errors.
var (
	// ErrCharacterNotFound means the account has no character with the requested
	// name and none could be created.
	ErrCharacterNotFound = errors.New("character not found")

	// ErrCharacterCreate means the server refused to create the character with
	// an error other than "name in use".
	ErrCharacterCreate = errors.New("character create rejected")
)

// Character identifies the character a session should play.
type Character struct {
	GUID  uint64
	Name  string
	Race  uint8
	Class uint8
	Level uint8
}

// Appearance holds the cosmetic fields CMSG_CHAR_CREATE requires. The server
// validates them against the race/gender combination; the defaults below are
// valid for every playable race and are what upstream's e2e harness uses.
type Appearance struct {
	Gender     uint8
	Skin       uint8
	Face       uint8
	HairStyle  uint8
	HairColor  uint8
	FacialHair uint8
	OutfitID   uint8
}

// DefaultAppearance returns the appearance upstream's e2e harness creates
// characters with: every option index 1, hair colour 0, no outfit override.
func DefaultAppearance(gender uint8) Appearance {
	return Appearance{Gender: gender, Skin: 1, Face: 1, HairStyle: 1, HairColor: 0, FacialHair: 1}
}

// EnterWorldOptions configures the character-select handshake.
type EnterWorldOptions struct {
	// Name is the desired character name. When the account has no character with
	// this name, one is created.
	Name string

	// Race and Class are used only when a character has to be created.
	Race  uint8
	Class uint8

	// Appearance is used only when a character has to be created.
	Appearance Appearance

	// NameAttempts bounds how many name variants are tried when the preferred
	// name is taken. Each attempt costs one create round trip.
	NameAttempts int

	// CharEnumTimeout bounds waiting for SMSG_CHAR_ENUM.
	CharEnumTimeout time.Duration

	// CharCreateTimeout bounds waiting for SMSG_CHAR_CREATE.
	CharCreateTimeout time.Duration

	// LoginTimeout bounds waiting for SMSG_LOGIN_VERIFY_WORLD.
	LoginTimeout time.Duration

	// Logf receives progress lines. Nil discards them.
	Logf func(format string, args ...interface{})
}

func (o EnterWorldOptions) withDefaults() EnterWorldOptions {
	if o.NameAttempts <= 0 {
		o.NameAttempts = 5
	}
	if o.CharEnumTimeout <= 0 {
		o.CharEnumTimeout = 15 * time.Second
	}
	if o.CharCreateTimeout <= 0 {
		o.CharCreateTimeout = 20 * time.Second
	}
	if o.LoginTimeout <= 0 {
		o.LoginTimeout = 60 * time.Second
	}
	if o.Appearance == (Appearance{}) {
		o.Appearance = DefaultAppearance(0)
	}
	if o.Logf == nil {
		o.Logf = func(string, ...interface{}) {}
	}
	return o
}

// EnterWorld performs the character-select handshake and leaves the session in
// PhaseInWorld.
//
// Sequence, every step of which is a server precondition:
//
//	SendReadyForAccountDataTimes -> SendRealmSplit -> RequestCharList
//	  -> [CreateCharacter until the server accepts a name]
//	  -> LoginCharacter -> WaitForLogin (SMSG_LOGIN_VERIFY_WORLD)
//
// Omitting the first two makes CMSG_CHAR_ENUM silently do nothing, which looks
// exactly like a server that never replies.
//
// The character is matched by name and never by position. Falling back to
// chars[0] would quietly log in a different character than the caller asked for,
// which is precisely the cross-contamination the project rules forbid.
func (s *Session) EnterWorld(ctx context.Context, opt EnterWorldOptions) (Character, error) {
	opt = opt.withDefaults()

	// Drop anything left over from an earlier handshake on this socket.
	s.drainCharLists()
	s.drainCharCreate()

	if err := s.doWithLock(s.client.SendReadyForAccountDataTimes); err != nil {
		return Character{}, fmt.Errorf("wow: CMSG_READY_FOR_ACCOUNT_DATA_TIMES: %w", err)
	}
	if err := s.doWithLock(s.client.SendRealmSplit); err != nil {
		return Character{}, fmt.Errorf("wow: CMSG_REALM_SPLIT: %w", err)
	}

	chars, err := s.requestCharList(ctx, opt.CharEnumTimeout)
	if err != nil {
		return Character{}, err
	}
	opt.Logf("account has %d character(s)", len(chars))

	char, found := findCharacter(chars, opt.Name)
	if !found {
		opt.Logf("no character named %q on this account, creating one", opt.Name)
		char, err = s.createCharacter(ctx, opt)
		if err != nil {
			return Character{}, err
		}
	}

	// Race drives chat language selection; without it a Horde bot cannot speak
	// the language AzerothGhost uses by default.
	if char.Race != 0 {
		s.client.SetCharRace(char.Race)
	}

	// LoginCharacter also records charGUID and precomputes the packed guid used
	// by later movement packets, so it must be used instead of a raw send.
	if err := s.doWithLock(func() error { return s.client.LoginCharacter(char.GUID) }); err != nil {
		return Character{}, fmt.Errorf("wow: login character %q: %w", char.Name, err)
	}

	// WaitForLogin closes on SMSG_LOGIN_VERIFY_WORLD, which is also the moment
	// the client transitions to PhaseInWorld.
	if err := waitUpstream(ctx, "SMSG_LOGIN_VERIFY_WORLD", opt.LoginTimeout, s.client.WaitForLogin); err != nil {
		return Character{}, err
	}

	opt.Logf("entered world as %s (guid=%d race=%d class=%d level=%d)",
		char.Name, char.GUID, char.Race, char.Class, char.Level)
	return char, nil
}

// deliverCharList is the SmsgCharEnum callback. It runs on the client's read
// goroutine and must never block.
func (s *Session) deliverCharList(chars []client.CharEnumEntry) {
	// The client owns the slice it parsed; take a copy before queueing it.
	cp := append([]client.CharEnumEntry(nil), chars...)

	select {
	case s.charLists <- cp:
		return
	default:
	}
	// Buffer full: discard the oldest list so a waiter always observes the
	// newest one. A stale list is worse than no list, because the caller would
	// conclude a character does not exist.
	select {
	case <-s.charLists:
	default:
	}
	select {
	case s.charLists <- cp:
	default:
	}
}

// deliverCharCreate is the SmsgCharCreate callback. It runs on the client's read
// goroutine and must never block.
func (s *Session) deliverCharCreate(data []byte) {
	if len(data) == 0 {
		return
	}
	code := CharCreateResult(data[0])

	select {
	case s.charCreate <- code:
		return
	default:
	}
	// Same reasoning as deliverCharList: keep the newest reply.
	select {
	case <-s.charCreate:
	default:
	}
	select {
	case s.charCreate <- code:
	default:
	}
}

func (s *Session) drainCharLists() {
	for {
		select {
		case <-s.charLists:
		default:
			return
		}
	}
}

func (s *Session) drainCharCreate() {
	for {
		select {
		case <-s.charCreate:
		default:
			return
		}
	}
}

// requestCharList sends CMSG_CHAR_ENUM and waits for SMSG_CHAR_ENUM.
//
// This is a pure signal wait, not an Exchange: the character list arrives
// through AzerothGhost's OnCharList callback rather than the opcode dispatcher.
func (s *Session) requestCharList(ctx context.Context, timeout time.Duration) ([]client.CharEnumEntry, error) {
	s.drainCharLists()

	if err := s.doWithLock(s.client.RequestCharList); err != nil {
		return nil, fmt.Errorf("wow: CMSG_CHAR_ENUM: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case chars := <-s.charLists:
		return chars, nil
	case <-timer.C:
		return nil, fmt.Errorf("wow: SMSG_CHAR_ENUM: %w", ErrTimeout)
	case <-ctx.Done():
		return nil, fmt.Errorf("wow: SMSG_CHAR_ENUM: %w", ctx.Err())
	}
}

// createCharacter tries each candidate name until the server accepts one, then
// re-enumerates to learn its GUID.
//
// The reply channel is drained before every attempt so a late reply from a
// previous attempt can never be read as this attempt's outcome.
func (s *Session) createCharacter(ctx context.Context, opt EnterWorldOptions) (Character, error) {
	names := GenerateNames(opt.Name, opt.NameAttempts-1)

	var lastCode CharCreateResult
	for _, name := range names {
		s.drainCharCreate()

		create := func() error {
			return s.client.CreateCharacter(name, opt.Race, opt.Class, opt.Appearance.Gender,
				opt.Appearance.Skin, opt.Appearance.Face, opt.Appearance.HairStyle,
				opt.Appearance.HairColor, opt.Appearance.FacialHair, opt.Appearance.OutfitID)
		}
		if err := s.doWithLock(create); err != nil {
			return Character{}, fmt.Errorf("wow: CMSG_CHAR_CREATE %q: %w", name, err)
		}

		code, err := s.waitCharCreate(ctx, opt.CharCreateTimeout)
		if err != nil {
			// A missing reply under load does not prove the create failed, but
			// assuming success would be worse. Move to the next name: the next
			// enum will reveal whether this one actually landed.
			opt.Logf("no create result for %q (%v), trying the next candidate name", name, err)
			continue
		}

		switch code {
		case CharCreateSuccess:
			return s.characterByName(ctx, name, opt)
		case CharCreateNameInUse:
			lastCode = code
			opt.Logf("character name %q is taken, trying the next candidate", name)
		default:
			// Anything else is terminal: a disabled realm, a class/race the
			// server refuses, an expansion or level gate, or a name-validation
			// code our own generator should never have produced. Retrying other
			// names would just repeat the same failure, and hiding a generator
			// bug behind retries would make it much harder to find.
			//
			// Note for anyone comparing with upstream's harness: it also treats
			// code 0x00 as success, which is not an AzerothCore response code at
			// all — the only success path is `SendCharCreate(CHAR_CREATE_SUCCESS)`
			// (CharacterHandler.cpp:594). Accepting 0x00 here would silently
			// accept a truncated or mis-framed packet into the login flow.
			//
			// The code renders through CharCreateResult.String, which names known
			// codes and keeps the byte for unknown ones, so the log line alone is
			// enough to look the cause up.
			return Character{}, fmt.Errorf("wow: create character %q: server returned %s: %w",
				name, code, ErrCharacterCreate)
		}
	}

	if lastCode == CharCreateNameInUse {
		return Character{}, fmt.Errorf("wow: %d candidate name(s) starting from %q are all taken: %w",
			len(names), names[0], ErrCharacterCreate)
	}
	return Character{}, fmt.Errorf("wow: no create result for %d candidate name(s) starting from %q: %w",
		len(names), names[0], ErrTimeout)
}

// waitCharCreate waits for the reply to the create that was just sent.
func (s *Session) waitCharCreate(ctx context.Context, timeout time.Duration) (CharCreateResult, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case code := <-s.charCreate:
		return code, nil
	case <-timer.C:
		return 0, ErrTimeout
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// characterByName re-enumerates the account to fetch the created character. The
// create reply carries no GUID, so SMSG_CHAR_ENUM is the only way to learn it.
func (s *Session) characterByName(ctx context.Context, name string, opt EnterWorldOptions) (Character, error) {
	chars, err := s.requestCharList(ctx, opt.CharEnumTimeout)
	if err != nil {
		return Character{}, fmt.Errorf("wow: character list after creating %q: %w", name, err)
	}
	char, found := findCharacter(chars, name)
	if !found {
		return Character{}, fmt.Errorf("wow: created character %q is missing from SMSG_CHAR_ENUM: %w",
			name, ErrCharacterNotFound)
	}
	return char, nil
}

// findCharacter matches by name, case-insensitively, the way the server does.
func findCharacter(chars []client.CharEnumEntry, name string) (Character, bool) {
	for _, c := range chars {
		if strings.EqualFold(c.Name, name) {
			return Character{GUID: c.GUID, Name: c.Name, Race: c.Race, Class: c.Class, Level: c.Level}, true
		}
	}
	return Character{}, false
}

// charDeleteTimeout bounds waiting for SMSG_CHAR_DELETE.
const charDeleteTimeout = 15 * time.Second

// DeleteCharacter deletes one character from the account and waits for the
// server's verdict. It is the counterpart of the create path above and exists so
// that provisioning has a real cleanup: an account can only be removed once its
// characters are gone (wowdata.DeleteAccount enforces that).
//
// Wire format, both from AzerothCore:
//
//	CMSG_CHAR_DELETE = 0x038 — the payload is the raw little-endian uint64 GUID.
//	  CharacterHandler.cpp:614-617 reads it with `recvData >> guid`, and
//	  ObjectGuid.cpp:76-80 implements that as a plain uint64 read, so this packet
//	  is *not* packed (unlike movement packets).
//	SMSG_CHAR_DELETE = 0x03C — one uint8 ResponseCodes byte. Only
//	  CHAR_DELETE_SUCCESS (0x47) means the row is gone; the rest name the gate
//	  that refused (guild leader, arena captain, transfer lock).
//
// Send it from the character-select phase: the server refuses to delete a
// character that is still logged in, and closing a session does not unregister
// it instantly.
//
// A GUID the server does not know (not in its character cache) makes the handler
// return silently — no SMSG_CHAR_DELETE at all (CharacterHandler.cpp:652-658,
// where the account check fails with accountId still 0). That surfaces here as a
// timeout, never as a false success.
func (s *Session) DeleteCharacter(ctx context.Context, guid uint64, timeout time.Duration) error {
	if guid == 0 {
		return errors.New("wow: delete character: guid is zero")
	}
	if timeout <= 0 {
		timeout = charDeleteTimeout
	}

	// The client's own DeleteCharacter writes the same bytes; going through Do
	// here is what buys us the reply, which the upstream client never parses.
	var payload bytes.Buffer
	if err := binary.Write(&payload, binary.LittleEndian, guid); err != nil {
		return fmt.Errorf("wow: delete character %d: encode guid: %w", guid, err)
	}

	opcode, data, err := s.Do(ctx, CmsgCharDelete, payload.Bytes(), []uint16{SmsgCharDelete}, timeout)
	if err != nil {
		return fmt.Errorf("wow: delete character %d: %w", guid, err)
	}
	if opcode != SmsgCharDelete {
		return fmt.Errorf("wow: delete character %d: unexpected reply %s", guid, OpcodeName(opcode))
	}
	if len(data) == 0 {
		return fmt.Errorf("wow: delete character %d: empty %s", guid, OpcodeName(SmsgCharDelete))
	}

	if result := CharDeleteResult(data[0]); result != CharDeleteSuccess {
		return fmt.Errorf("wow: delete character %d: server answered %s", guid, result)
	}
	return nil
}
