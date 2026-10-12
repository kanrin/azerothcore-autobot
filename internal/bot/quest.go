package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"azerothcore-autobot/internal/agent"
	"azerothcore-autobot/internal/feature/quest"
	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/packet"
	"azerothcore-autobot/internal/wow/state"
	"azerothcore-autobot/internal/wowdata"

	"github.com/azerothcore/AzerothGhost/client"
)

// The questgiver adapter: it finds the NPC, opens the dialog, and takes the quest the
// picker chose.
//
// Like travel.go, this file is the only place that knows both a feature package and the
// protocol, and it depends on narrow interfaces (questSession, questWorld, giverFinder)
// rather than on the concrete session, so the flow is testable without a server.

const (
	// questScore sits above travel: reading a quest giver is local and quick, while a
	// walk is long, and a bot that reads its quests first is a bot that knows why it
	// is walking.
	questScore = 60

	// questInteractRange is slightly inside AzerothCore's INTERACTION_DISTANCE = 5.5f
	// (ObjectDefines.h:24), the radius Player::CanInteractWithQuestGiver checks
	// (Player.cpp:2158). Slightly inside because our position is the server's last
	// report, which can be older than the server's own view: asking from exactly 5.5
	// yards is asking to be closed out without a word.
	questInteractRange float32 = 5.0

	// questMaxGiversPerRun bounds how many NPCs one bot visits in a session, so a
	// misconfigured roster cannot turn into an endless tour of the zone.
	questMaxGiversPerRun = 5
)

// questSession is the part of wow.Session this file uses.
type questSession interface {
	QuestGiverHello(ctx context.Context, npcGUID uint64, timeout time.Duration) (wow.QuestGiverQuestion, error)
	QueryQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (packet.QuestDetails, error)
	AcceptQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) error
	QuestLog() []state.QuestSlot
}

// questWorld is the world knowledge this file uses. *wowdata.Client satisfies it.
type questWorld interface {
	QuestsOfferedBy(ctx context.Context, creatureEntry uint32) ([]uint32, error)
	QuestsByIDs(ctx context.Context, ids []uint32) (map[uint32]wowdata.Quest, error)
}

// nearbyUnit is one visible unit, reduced to what this file needs so that it does not
// carry the upstream client type into its tests.
type nearbyUnit struct {
	GUID  uint64
	Entry uint32
}

// giverFinder finds the NPCs to talk to. clientGivers adapts AzerothGhost's object
// cache to it.
type giverFinder interface {
	FindUnitByEntry(entry uint32, maxDist float32) uint64
	NearbyUnits(maxDist float32) []nearbyUnit
}

// clientGivers adapts *client.WorldClient's object cache to giverFinder.
type clientGivers struct{ w *client.WorldClient }

// FindUnitByEntry returns the nearest visible unit with that entry, or 0.
func (g clientGivers) FindUnitByEntry(entry uint32, maxDist float32) uint64 {
	if g.w == nil {
		return 0
	}
	return g.w.FindUnitByEntry(entry, maxDist)
}

// NearbyUnits lists the units within maxDist.
func (g clientGivers) NearbyUnits(maxDist float32) []nearbyUnit {
	if g.w == nil {
		return nil
	}
	objects := g.w.GetNearbyUnits(maxDist)
	units := make([]nearbyUnit, 0, len(objects))
	for _, o := range objects {
		units = append(units, nearbyUnit{GUID: o.GUID, Entry: o.Entry})
	}
	return units
}

// questBehavior takes quests from the NPCs around the bot.
type questBehavior struct {
	sess  questSession
	world questWorld
	units giverFinder

	logger *slog.Logger

	// giverEntry pins the NPC to talk to. Zero means "whichever questgiver is in
	// range", which is how a bot discovers work without being told where to look.
	giverEntry uint32

	// timeout bounds one questgiver round trip.
	timeout time.Duration

	// visited records the givers this run has talked to, so each is asked once instead
	// of every tick. The key is the NPC's guid, not its entry: two instances of the
	// same creature are two conversations.
	visited map[uint64]bool

	// offers caches the world knowledge per creature entry: a creature's quest list
	// does not change, and asking the database every tick would be silly.
	offers map[uint32][]uint32

	// accepted records what this run took, for the summary line.
	accepted []uint32
}

// Name identifies the behavior in logs and telemetry.
func (b *questBehavior) Name() string { return "quest" }

// Score wants to run while there is a giver in range this run has not visited yet.
//
// It cannot tell "in range" from "is actually a questgiver" without the database, and
// the database does not belong in Score — so the first tick on a non-giver does one
// lookup, learns better, and the score drops. One wasted tick, once.
func (b *questBehavior) Score(bb *agent.Blackboard) int {
	if len(b.visited) >= questMaxGiversPerRun {
		return 0
	}
	if _, ok := SelfOf(bb); !ok {
		return 0
	}
	if b.giverGUID() == 0 {
		return 0
	}
	return questScore
}

// Run talks to one giver: hello, pick, ask for the details, accept.
func (b *questBehavior) Run(ctx context.Context, bb *agent.Blackboard) error {
	self, ok := SelfOf(bb)
	if !ok || !self.InWorld {
		return errors.New("quest: the bot is not in the world")
	}

	guid := b.giverGUID()
	if guid == 0 {
		return agent.Done
	}

	list, err := b.sess.QuestGiverHello(ctx, guid, b.timeout)
	if err != nil {
		// A failed hello is a protocol or range problem, not "nothing to do": report it
		// so the agent backs this behavior off and the log says why.
		return fmt.Errorf("quest: open the dialog with npc %d: %w", guid, err)
	}
	b.visited[guid] = true
	b.logger.Info("quest: the npc answered", "npc", guid,
		"opcode", wow.OpcodeName(list.AnsweredWith), "quests", len(list.Quests),
		"greeting", list.Greeting)

	if len(list.Quests) == 0 {
		b.logger.Info("quest: the npc has nothing to offer", "npc", guid)
		return agent.Done
	}

	// World knowledge for everything the giver listed, so the picker can weigh a
	// quest's level against the bot's own.
	ids := make([]uint32, 0, len(list.Quests))
	for _, entry := range list.Quests {
		ids = append(ids, entry.ID)
	}
	templates, err := b.world.QuestsByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("quest: read the templates of %v: %w", ids, err)
	}

	candidates := make([]quest.Candidate, 0, len(ids))
	for _, entry := range list.Quests {
		candidate := quest.Candidate{ID: entry.ID, Title: entry.Title, Level: entry.Level,
			InLog: b.inLog(entry.ID)}
		if template, found := templates[entry.ID]; found {
			candidate.Title = template.Title
			candidate.Level = template.Level
			candidate.MinLevel = template.MinLevel
		} else {
			// The giver lists a quest with no template: a server data problem, and
			// guessing its requirements would be worse than reporting it.
			b.logger.Warn("quest: the npc offers a quest with no template", "npc", guid, "quest", entry.ID)
			candidate.MinLevel = 255
		}
		candidates = append(candidates, candidate)
	}

	pick, ok := quest.ChooseAcceptable(candidates, self.Level)
	if !ok {
		for _, skip := range quest.Explain(candidates, self.Level) {
			b.logger.Info("quest: skipping", "npc", guid, "quest", skip.Candidate.ID,
				"title", skip.Candidate.Title, "quest_level", skip.Candidate.Level,
				"min_level", skip.Candidate.MinLevel, "reason", skip.Reason)
		}
		return agent.Done
	}

	details, err := b.sess.QueryQuest(ctx, guid, pick.ID, b.timeout)
	if err != nil {
		return fmt.Errorf("quest: details of %d: %w", pick.ID, err)
	}
	b.logger.Info("quest: taking", "npc", guid, "quest", pick.ID, "title", details.Title,
		"level", pick.Level, "min_level", pick.MinLevel, "objectives", details.Objectives)

	if err := b.sess.AcceptQuest(ctx, guid, pick.ID, b.timeout); err != nil {
		// The interesting failure is a refusal: the requirements were not met, or the
		// server never added the quest. The template says what it wanted, so put that in
		// the error instead of leaving the caller with a bare "timeout".
		return fmt.Errorf("quest: accept %d (%s, needs level %d, bot is level %d): %w",
			pick.ID, pick.Title, pick.MinLevel, self.Level, err)
	}

	b.accepted = append(b.accepted, pick.ID)
	b.logger.Info("quest: accepted", "npc", guid, "quest", pick.ID, "title", details.Title,
		"quest_log_size", len(b.sess.QuestLog()), "accepted_this_run", b.accepted)
	return nil
}

// Stop has nothing to release: the behavior holds no route, no loot window and no
// pending packet exchange. It exists so that fact is written down rather than
// re-derived by whoever reads the agent next.
func (b *questBehavior) Stop(context.Context) error { return nil }

// giverGUID returns the NPC to talk to, or 0 when there is none in range or the one
// there has already been visited.
func (b *questBehavior) giverGUID() uint64 {
	if b.giverEntry != 0 {
		guid := b.units.FindUnitByEntry(b.giverEntry, questInteractRange)
		if guid == 0 || b.visited[guid] {
			return 0
		}
		return guid
	}

	for _, unit := range b.units.NearbyUnits(questInteractRange) {
		if b.visited[unit.GUID] || len(b.offersFor(unit.Entry)) == 0 {
			continue
		}
		return unit.GUID
	}
	return 0
}

// offersFor returns the quests a creature entry offers, cached.
//
// The cache is what keeps the auto-discovery path from querying the database for every
// unit in range on every tick. A lookup error yields an empty list: the next tick
// retries, where failing the behavior would turn a transient database problem into a
// bot that looks broken.
func (b *questBehavior) offersFor(entry uint32) []uint32 {
	if cached, ok := b.offers[entry]; ok {
		return cached
	}
	ids, err := b.world.QuestsOfferedBy(context.Background(), entry)
	if err != nil {
		b.logger.Warn("quest: cannot read the quests of a creature", "entry", entry, "err", err.Error())
		ids = nil
	}
	b.offers[entry] = ids
	return ids
}

// inLog reports whether the quest is already in the bot's log.
func (b *questBehavior) inLog(questID uint32) bool {
	for _, slot := range b.sess.QuestLog() {
		if slot.QuestID == questID {
			return true
		}
	}
	return false
}

// newQuestBehavior wires the adapter.
func newQuestBehavior(sess questSession, world questWorld, units giverFinder,
	logger *slog.Logger, giverEntry uint32, timeout time.Duration) *questBehavior {

	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &questBehavior{
		sess:       sess,
		world:      world,
		units:      units,
		logger:     logger,
		giverEntry: giverEntry,
		timeout:    timeout,
		visited:    map[uint64]bool{},
		offers:     map[uint32][]uint32{},
	}
}
