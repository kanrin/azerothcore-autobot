package quest

// LogEntry is one quest in the bot's log, reduced to what the hand-in decision needs.
type LogEntry struct {
	ID uint32

	// Complete is the server's own "objectives done" flag — QUEST_STATE_COMPLETE in the
	// quest log (Player.h:620), which is what draws the yellow question mark.
	Complete bool
}

// ChooseTurnIn picks which quest to hand in at a giver.
//
// endedHere says which quest ids this NPC accepts a hand-in for (creature_questender).
// Only a quest that is both in the log and complete is a candidate: the server checks
// exactly that and answers nothing at all when it does not hold (QuestHandler.cpp:488-521),
// so guessing a quest would spend a round trip and leave the bot looking stuck.
//
// The lowest quest id wins, so two runs over the same log behave the same.
func ChooseTurnIn(log []LogEntry, endedHere map[uint32]bool) (uint32, bool) {
	best := uint32(0)
	for _, entry := range log {
		if !entry.Complete || !endedHere[entry.ID] {
			continue
		}
		if best == 0 || entry.ID < best {
			best = entry.ID
		}
	}
	return best, best != 0
}

// ExplainTurnIn reports why each log entry is or is not being handed in here, in the
// same spirit as Explain: "the NPC does not end this quest" and "the objectives are not
// done" look identical in a log otherwise.
func ExplainTurnIn(log []LogEntry, endedHere map[uint32]bool) []Skip {
	chosen, ok := ChooseTurnIn(log, endedHere)

	rows := make([]Skip, 0, len(log))
	for _, entry := range log {
		candidate := Candidate{ID: entry.ID}
		switch {
		case ok && entry.ID == chosen:
			rows = append(rows, Skip{Candidate: candidate, Reason: "chosen"})
		case !endedHere[entry.ID]:
			rows = append(rows, Skip{Candidate: candidate, Reason: "this npc does not end that quest"})
		case !entry.Complete:
			rows = append(rows, Skip{Candidate: candidate, Reason: "objectives not done yet"})
		default:
			rows = append(rows, Skip{Candidate: candidate, Reason: "lower quest id preferred"})
		}
	}
	return rows
}
