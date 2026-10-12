//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/packet"

	"github.com/azerothcore/AzerothGhost/e2e/e2eharness"
)

// The quest probe account. Its own namespace, so it can be cleaned up without
// touching anything else: "autoqst" is the prefix, the character name carries its
// letters.
const (
	questAccount   = "autoqst01"
	questPassword  = "autoqstpw"
	questCharacter = "Autoqstp"
)

// willemEntry is Deputy Willem. He stands 2.3 yards from the human starting position
// (creature.position -8947.64,-132.32,83.72 vs playercreateinfo -8949.95,-132.49,83.53),
// which is why the first questing tests need no pathfinding at all.
const willemEntry = 823

// TestQuestGiverHelloOpcodeTrace pins what the server actually answers a questgiver
// hello with.
//
// This is a regression test with a diagnosis baked in. The AC source has *two* paths
// in HandleQuestgiverHelloOpcode (QuestHandler.cpp:79-110): PrepareGossipMenu(+quest
// menu) followed by SendPreparedGossip, and Player::PrepareQuestMenu either sends
// SMSG_QUESTGIVER_QUEST_LIST (many quests, PlayerQuest.cpp:199) or
// SMSG_QUESTGIVER_QUEST_DETAILS (exactly one, PlayerQuest.cpp:148). Which one a given
// NPC produces is server data, not a constant, so the bot cannot assume the list.
//
// The first live run of the quest behaviour assumed the list and timed out. This test
// records what came back instead, by hooking every opcode the client receives.
func TestQuestGiverHelloOpcodeTrace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	charDB := openDB(t, "characters", e2eharness.CharDSN)
	authDB := openDB(t, "auth", e2eharness.AuthDSN)
	data := openData(t, questProbePrefix())

	ensureQuestProbeAccount(t, ctx, authDB)
	sess, guid := loginQuestProbe(t, ctx)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cleanupQuestProbe(t, cleanupCtx, data, charDB)
	})

	// The character logs out before the cleanup runs: the server refuses to delete a
	// character it still considers online, and a socket that was closed a moment ago
	// is not necessarily noticed yet.
	defer func() {
		if err := sess.Logout(ctx, 40*time.Second); err != nil {
			t.Logf("logout: %v", err)
		}
		sess.Close()
	}()

	// The nearby units arrive in the initial update batches, which can land a moment
	// after the character's own update fields. Wait for Willem to be in the cache
	// rather than racing it.
	npc := waitForUnitEntry(t, sess, willemEntry, 5, 20*time.Second)
	t.Logf("Deputy Willem is guid %d; the probe character is guid %d", npc, guid)

	// Every quest-related reply is interesting; everything else is counted so the
	// failure message can say whether the connection was alive at all.
	interesting := map[uint16]string{
		wow.SmsgQuestgiverQuestList:    "SMSG_QUESTGIVER_QUEST_LIST",
		wow.SmsgQuestgiverQuestDetails: "SMSG_QUESTGIVER_QUEST_DETAILS",
		wow.SmsgGossipMessage:          "SMSG_GOSSIP_MESSAGE",
		wow.SmsgGossipComplete:         "SMSG_GOSSIP_COMPLETE",
		wow.SmsgQuestgiverRequestItems: "SMSG_QUESTGIVER_REQUEST_ITEMS",
	}
	replies := make(chan uint16, 8)
	seen := make(chan string, 64)

	cancelHook := sess.Client().AddPacketHook(func(opcode uint16, payload []byte) {
		if _, ok := interesting[opcode]; ok {
			select {
			case replies <- opcode:
			default:
			}
		}
		select {
		case seen <- fmt.Sprintf("%s %d bytes", wow.OpcodeName(opcode), len(payload)):
		default:
		}
	})
	defer cancelHook()

	if err := sess.Send(wow.CmsgQuestgiverHello, packet.EncodeQuestgiverHello(npc)); err != nil {
		t.Fatalf("send CMSG_QUESTGIVER_HELLO: %v", err)
	}

	// Wait for a quest-related reply. The timeout is an upper bound on the wait, not
	// an assertion about how fast the server is; the assertion is on the packet.
	select {
	case opcode := <-replies:
		t.Logf("the server answered CMSG_QUESTGIVER_HELLO with %s", wow.OpcodeName(opcode))
		if opcode == wow.SmsgGossipMessage {
			t.Logf("that is the gossip path: the quest list has to be requested with " +
				"CMSG_GOSSIP_SELECT_OPTION (GOSSIP_OPTION_QUESTGIVER = 2, GossipDef.h:37)")
		}
	case <-time.After(10 * time.Second):
		var received []string
		for {
			select {
			case s := <-seen:
				received = append(received, s)
			default:
				t.Fatalf("no quest-related reply to CMSG_QUESTGIVER_HELLO within 10s; "+
					"the client did receive %d other packets, the last ones: %v",
					len(received), tail(received, 5))
			}
		}
	}
}

// waitForUnitEntry waits until a unit with that entry is visible within maxDist, and
// fails the test with a precondition message when it never shows up. The wait is on a
// real signal (the object cache), which is also what the quest behaviour needs.
func waitForUnitEntry(t *testing.T, sess *wow.Session, entry uint32, maxDist float32, timeout time.Duration) uint64 {
	t.Helper()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if guid := sess.Client().FindUnitByEntry(entry, maxDist); guid != 0 {
			return guid
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			x, y, z, _, mapID := sess.Position()
			t.Fatalf("precondition: creature %d is not within %.1f yards of the character "+
				"(map %d at %.1f,%.1f,%.1f) after %s", entry, maxDist, mapID, x, y, z, timeout)
			return 0
		}
	}
}

// tail returns the last n entries of s, for a readable failure message.
func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// questProbePrefix is the namespace of the quest probe account.
func questProbePrefix() string { return "autoqst" }
