// Package quest decides what a bot should do about quests.
//
// It is a pure planner: it holds no session, sends nothing and reads no database.
// Everything it needs arrives as arguments — the quests a giver offers, what the
// world knows about them, and what is already in the bot's log — which is what makes
// the choice testable without a server and what keeps the "walk up, talk, accept"
// part in internal/bot, where the protocol lives.
package quest

import "sort"

// Candidate is one quest a giver offers, with the world knowledge attached.
type Candidate struct {
	ID    uint32
	Title string

	// Level is the quest's own level; low is good for a bot that wants progress it
	// can actually finish.
	Level int32

	// MinLevel is what the player must have to be allowed to take it.
	MinLevel uint8

	// InLog reports that the bot is already on this quest. Taking it again is not a
	// thing, and re-asking makes the bot look stuck rather than busy.
	InLog bool
}

// Takeable reports whether the quest can be accepted at this level.
func (c Candidate) Takeable(playerLevel uint8) bool {
	return !c.InLog && playerLevel >= c.MinLevel
}

// ChooseAcceptable picks the quest to take from what a giver offers.
//
// The order is: takeable first, then lowest quest level, then lowest id. Lowest level
// rather than highest because a bot that finishes what it accepts makes progress,
// while one that keeps taking quests it cannot finish stands still. The id is the
// tie-break so that two runs over the same data pick the same quest — a planner whose
// choice depends on map order is impossible to debug.
//
// The second return value is false when nothing is takeable, which the caller reports
// rather than retrying: the giver will not suddenly offer something else.
func ChooseAcceptable(candidates []Candidate, playerLevel uint8) (Candidate, bool) {
	best := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Takeable(playerLevel) {
			best = append(best, c)
		}
	}
	if len(best) == 0 {
		return Candidate{}, false
	}

	sort.Slice(best, func(i, j int) bool {
		if best[i].Level != best[j].Level {
			return best[i].Level < best[j].Level
		}
		return best[i].ID < best[j].ID
	})
	return best[0], true
}

// Skip is one candidate that was not chosen, with the reason. It exists so the bot
// can log *why* it ignored what a giver offered — the difference between "the NPC has
// nothing for me" and "everything it has is too high level" is what tells an operator
// whether the bot needs to level up or the roster entry is wrong.
type Skip struct {
	Candidate Candidate
	Reason    string
}

// Explain returns the candidates in the same order ChooseAcceptable considers them,
// each with the reason it was or was not chosen. The chosen one is marked "chosen".
func Explain(candidates []Candidate, playerLevel uint8) []Skip {
	skip := make([]Skip, 0, len(candidates))
	chosen, ok := ChooseAcceptable(candidates, playerLevel)

	for _, c := range candidates {
		switch {
		case ok && c.ID == chosen.ID:
			skip = append(skip, Skip{Candidate: c, Reason: "chosen"})
		case c.InLog:
			skip = append(skip, Skip{Candidate: c, Reason: "already in the quest log"})
		case playerLevel < c.MinLevel:
			skip = append(skip, Skip{Candidate: c, Reason: "needs level " + itoa(int(c.MinLevel))})
		default:
			skip = append(skip, Skip{Candidate: c, Reason: "lower level quest preferred"})
		}
	}
	return skip
}

// itoa avoids pulling strconv in for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
