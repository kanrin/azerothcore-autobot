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

	// questMaxVisitsPerGiver is how many conversations one NPC gets. Two, because a
	// visit can hand in a finished quest and then ask what else there is, and a quest
	// that cannot be handed in yet may become handable later in the run (a kill counted
	// by combat, for instance).
	questMaxVisitsPerGiver = 2
)

// giver is the NPC a visit talks to: the guid to address and the entry to look its
// quest relations up by.
type giver struct {
	GUID  uint64
	Entry uint32
}

// questSession is the part of wow.Session this file uses.
type questSession interface {
	QuestGiverHello(ctx context.Context, npcGUID uint64, timeout time.Duration) (wow.QuestGiverQuestion, error)
	QueryQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (packet.QuestDetails, error)
	AcceptQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) error
	CompleteQuest(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (wow.QuestTurnIn, error)
	RequestReward(ctx context.Context, npcGUID uint64, questID uint32, timeout time.Duration) (wow.QuestTurnIn, error)
	ChooseReward(ctx context.Context, npcGUID uint64, questID, reward uint32, timeout time.Duration) (packet.QuestReward, error)
	QuestLog() []state.QuestSlot
}

// questWorld is the world knowledge this file uses. *wowdata.Client satisfies it.
type questWorld interface {
	QuestsOfferedBy(ctx context.Context, creatureEntry uint32) ([]uint32, error)
	QuestsEndedBy(ctx context.Context, creatureEntry uint32) ([]uint32, error)
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

	// visits counts how many conversations each giver has had, so one is asked again
	// only for a reason (see questMaxVisitsPerGiver) instead of every tick. The key is
	// the NPC's guid, not its entry: two instances of the same creature are two
	// conversations.
	visits map[uint64]int

	// offers and ends cache the world knowledge per creature entry: a creature's quest
	// relations do not change, and asking the database every tick would be silly.
	offers map[uint32][]uint32
	ends   map[uint32][]uint32

	// accepted and rewarded record what this run did, for the summary line.
	accepted []uint32
	rewarded []uint32
}

// Name identifies the behavior in logs and telemetry.
func (b *questBehavior) Name() string { return "quest" }

// Score wants to run while there is a giver in range that this run has not finished
// with, and while the run's conversation budget lasts.
func (b *questBehavior) Score(bb *agent.Blackboard) int {
	if _, ok := SelfOf(bb); !ok {
		return 0
	}

	conversations := 0
	for _, count := range b.visits {
		conversations += count
	}
	if conversations >= questMaxGiversPerRun*questMaxVisitsPerGiver {
		return 0
	}

	if _, ok := b.giver(); !ok {
		return 0
	}
	return questScore
}

// Run visits one giver: hand in what is finished, then take what is new.
//
// One conversation can do both, which is what a player does — and the visit budget
// (questMaxVisitsPerGiver) exists because a quest that cannot be handed in yet may become
// handable later in the same run.
func (b *questBehavior) Run(ctx context.Context, bb *agent.Blackboard) error {
	self, ok := SelfOf(bb)
	if !ok || !self.InWorld {
		return errors.New("quest: the bot is not in the world")
	}

	target, ok := b.giver()
	if !ok {
		return agent.Done
	}
	b.visits[target.GUID]++

	if err := b.turnIn(ctx, target); err != nil {
		return err
	}
	return b.accept(ctx, target, self)
}

// turnIn hands in one finished quest at a giver that ends it, if the bot has one.
func (b *questBehavior) turnIn(ctx context.Context, target giver) error {
	log := b.sess.QuestLog()
	if len(log) == 0 {
		return nil
	}

	ended := map[uint32]bool{}
	for _, id := range b.endsFor(target.Entry) {
		ended[id] = true
	}

	entries := make([]quest.LogEntry, 0, len(log))
	complete := map[uint32]bool{}
	for _, slot := range log {
		entries = append(entries, quest.LogEntry{ID: slot.QuestID, Complete: slot.IsComplete()})
		complete[slot.QuestID] = slot.IsComplete()
	}

	questID, ok := quest.ChooseTurnIn(entries, ended)
	if !ok {
		for _, skip := range quest.ExplainTurnIn(entries, ended) {
			b.logger.Info("quest: not handing in", "npc", target.GUID,
				"quest", skip.Candidate.ID, "reason", skip.Reason)
		}
		return nil
	}

	turnIn, err := b.sess.CompleteQuest(ctx, target.GUID, questID, b.timeout)
	if err != nil {
		return fmt.Errorf("quest: hand in %d at npc %d: %w", questID, target.GUID, err)
	}
	b.logger.Info("quest: handed in", "npc", target.GUID, "quest", questID, "title", turnIn.Title,
		"opcode", wow.OpcodeName(turnIn.AnsweredWith), "ready", turnIn.Ready,
		"choices", turnIn.Choices, "text", turnIn.Text)

	// A quest that requires items answers the hand-in with the items dialog even when
	// everything is in hand: the reward then has to be asked for separately, which is what
	// a client's "Complete Quest" button does (QuestHandler.cpp:517-518 then :355-379).
	if !turnIn.Ready && complete[questID] {
		offer, rewardErr := b.sess.RequestReward(ctx, target.GUID, questID, b.timeout)
		if rewardErr != nil {
			return fmt.Errorf("quest: ask for the reward of %d: %w", questID, rewardErr)
		}
		b.logger.Info("quest: asked for the reward", "npc", target.GUID, "quest", questID,
			"opcode", wow.OpcodeName(offer.AnsweredWith), "ready", offer.Ready,
			"choices", offer.Choices, "text", offer.Text)
		turnIn = offer
	}

	if !turnIn.Ready {
		// The server answered with what is still missing. Nothing to do about it here, and
		// the text it sent is already in the log line above.
		return nil
	}

	// Reward 0 is the first option and the only sensible choice when there is none:
	// AzerothCore rejects 6 and up (QuestHandler.cpp:255-263).
	reward, err := b.sess.ChooseReward(ctx, target.GUID, questID, 0, b.timeout)
	if err != nil {
		return fmt.Errorf("quest: choose the reward for %d: %w", questID, err)
	}
	b.rewarded = append(b.rewarded, questID)
	b.logger.Info("quest: rewarded", "npc", target.GUID, "quest", questID,
		"experience", reward.Experience, "money", reward.Money, "honor", reward.Honor,
		"talent_points", reward.TalentPoints, "rewarded_this_run", b.rewarded)
	return nil
}

// accept takes the best quest the giver has that the bot can do.
func (b *questBehavior) accept(ctx context.Context, target giver, self Self) error {
	list, err := b.sess.QuestGiverHello(ctx, target.GUID, b.timeout)
	if err != nil {
		// A failed hello is a protocol or range problem, not "nothing to do": report it
		// so the agent backs this behavior off and the log says why.
		return fmt.Errorf("quest: open the dialog with npc %d: %w", target.GUID, err)
	}
	b.logger.Info("quest: the npc answered", "npc", target.GUID,
		"opcode", wow.OpcodeName(list.AnsweredWith), "quests", len(list.Quests),
		"greeting", list.Greeting)

	if len(list.Quests) == 0 {
		b.logger.Info("quest: the npc has nothing to offer", "npc", target.GUID)
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
			b.logger.Warn("quest: the npc offers a quest with no template", "npc", target.GUID,
				"quest", entry.ID)
			candidate.MinLevel = 255
		}
		candidates = append(candidates, candidate)
	}

	pick, ok := quest.ChooseAcceptable(candidates, self.Level)
	if !ok {
		for _, skip := range quest.Explain(candidates, self.Level) {
			b.logger.Info("quest: skipping", "npc", target.GUID, "quest", skip.Candidate.ID,
				"title", skip.Candidate.Title, "quest_level", skip.Candidate.Level,
				"min_level", skip.Candidate.MinLevel, "reason", skip.Reason)
		}
		return agent.Done
	}

	details, err := b.sess.QueryQuest(ctx, target.GUID, pick.ID, b.timeout)
	if err != nil {
		return fmt.Errorf("quest: details of %d: %w", pick.ID, err)
	}
	b.logger.Info("quest: taking", "npc", target.GUID, "quest", pick.ID, "title", details.Title,
		"level", pick.Level, "min_level", pick.MinLevel, "objectives", details.Objectives)

	if err := b.sess.AcceptQuest(ctx, target.GUID, pick.ID, b.timeout); err != nil {
		// The interesting failure is a refusal: the requirements were not met, or the
		// server never added the quest. The template says what it wanted, so put that in
		// the error instead of leaving the caller with a bare "timeout".
		return fmt.Errorf("quest: accept %d (%s, needs level %d, bot is level %d): %w",
			pick.ID, pick.Title, pick.MinLevel, self.Level, err)
	}

	b.accepted = append(b.accepted, pick.ID)
	b.logger.Info("quest: accepted", "npc", target.GUID, "quest", pick.ID, "title", details.Title,
		"quest_log_size", len(b.sess.QuestLog()), "accepted_this_run", b.accepted)
	return nil
}

// Stop has nothing to release: the behavior holds no route, no loot window and no
// pending packet exchange. It exists so that fact is written down rather than
// re-derived by whoever reads the agent next.
func (b *questBehavior) Stop(context.Context) error { return nil }

// giver returns the NPC to talk to, or false when there is none in range that still has
// visits left.
//
// Without a configured entry it asks the world database which nearby creature offers or
// ends anything — that is how a bot discovers work without being told where to look. The
// entry→relations caches make the second call free, and a lookup error yields an empty
// list so the next tick retries rather than the behavior failing.
func (b *questBehavior) giver() (giver, bool) {
	if b.giverEntry != 0 {
		guid := b.units.FindUnitByEntry(b.giverEntry, questInteractRange)
		if guid == 0 || b.visits[guid] >= questMaxVisitsPerGiver {
			return giver{}, false
		}
		return giver{GUID: guid, Entry: b.giverEntry}, true
	}

	for _, unit := range b.units.NearbyUnits(questInteractRange) {
		if b.visits[unit.GUID] >= questMaxVisitsPerGiver {
			continue
		}
		if len(b.offersFor(unit.Entry)) == 0 && len(b.endsFor(unit.Entry)) == 0 {
			continue
		}
		return giver{GUID: unit.GUID, Entry: unit.Entry}, true
	}
	return giver{}, false
}

// offersFor returns the quests a creature entry offers, cached.
func (b *questBehavior) offersFor(entry uint32) []uint32 {
	if cached, ok := b.offers[entry]; ok {
		return cached
	}
	ids, err := b.world.QuestsOfferedBy(context.Background(), entry)
	if err != nil {
		b.logger.Warn("quest: cannot read the quests a creature offers", "entry", entry, "err", err.Error())
		ids = nil
	}
	b.offers[entry] = ids
	return ids
}

// endsFor returns the quests a creature entry accepts a hand-in for, cached.
func (b *questBehavior) endsFor(entry uint32) []uint32 {
	if cached, ok := b.ends[entry]; ok {
		return cached
	}
	ids, err := b.world.QuestsEndedBy(context.Background(), entry)
	if err != nil {
		b.logger.Warn("quest: cannot read the quests a creature ends", "entry", entry, "err", err.Error())
		ids = nil
	}
	b.ends[entry] = ids
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
		visits:     map[uint64]int{},
		offers:     map[uint32][]uint32{},
		ends:       map[uint32][]uint32{},
	}
}
