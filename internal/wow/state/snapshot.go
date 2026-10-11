package state

import (
	"fmt"
	"strings"

	"github.com/azerothcore/AzerothGhost/client"
)

// Skill is one PLAYER_SKILL_INFO_1_1 entry.
type Skill struct {
	Line  uint16 // skill line id; 0 marks an unused slot and is never returned
	Step  uint16 // rank tier the server keeps beside the line
	Value uint16
	Max   uint16

	// TempBonus and PermBonus are signed in AzerothCore (Player.h:87-89).
	TempBonus int16
	PermBonus int16
}

// Total is Value plus both bonuses: the number the character sheet shows and the
// number a profession requirement has to be compared against.
func (s Skill) Total() int { return int(s.Value) + int(s.TempBonus) + int(s.PermBonus) }

// readSkills unpacks PLAYER_SKILL_INFO_1_1.
//
// AzerothCore writes three fields per skill (Player.h:79-81) and clears all
// three when a skill is unlearned, so a zero line id means "empty slot"
// (Player.cpp:5539-5541).
func readSkills(self *client.WorldObject) []Skill {
	skills := make([]Skill, 0, 8)
	for i := 0; i < maxSkills; i++ {
		base := playerSkillInfo11 + uint16(i)*3
		line := self.Value(base)
		if line == 0 {
			continue
		}
		value := self.Value(base + 1)
		bonus := self.Value(base + 2)
		skills = append(skills, Skill{
			Line:      lowHalf(line),
			Step:      highHalf(line),
			Value:     lowHalf(value),
			Max:       highHalf(value),
			TempBonus: int16(lowHalf(bonus)),
			PermBonus: int16(highHalf(bonus)),
		})
	}
	return skills
}

// Skill returns the skill with that line id, if the bot has it.
func (snap Snapshot) Skill(line uint16) (Skill, bool) {
	for _, s := range snap.Skills {
		if s.Line == line {
			return s, true
		}
	}
	return Skill{}, false
}

// Count returns how many items of that entry the bot carries, summing stacks in
// every container (bank included). Items whose entry is not known yet count as
// zero, because their entry cannot be attributed.
func (snap Snapshot) Count(entry uint32) uint32 {
	if entry == 0 {
		return 0
	}
	var total uint32
	for _, item := range snap.Items {
		if item.Entry != entry || !item.Known {
			continue
		}
		count := item.Count
		if count == 0 {
			count = 1 // 3.3.5a stores a single item as a stack count of 0
		}
		total += count
	}
	return total
}

// FreeBagSlots returns the free slots in the backpack and the equipped bags. It
// is the number a "bags are full" check compares against.
func (snap Snapshot) FreeBagSlots() int {
	free := 0
	for _, c := range snap.Containers {
		if int(c.Capacity) > int(c.Used) {
			free += int(c.Capacity) - int(c.Used)
		}
	}
	return free
}

// UnknownEntries returns the GUIDs of occupied slots whose item object is not in
// the cache yet. A caller that has to act on the contents waits for these
// instead of guessing, since a teleport empties the cache.
func (snap Snapshot) UnknownEntries() []uint64 {
	var guids []uint64
	for _, item := range snap.Items {
		if !item.Known {
			guids = append(guids, item.GUID)
		}
	}
	return guids
}

// String summarises the snapshot on one line for logs.
func (snap Snapshot) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "money=%d items=%d free_bag_slots=%d skills=", snap.Money,
		len(snap.Items), snap.FreeBagSlots())
	if len(snap.Skills) == 0 {
		b.WriteString("none")
		return b.String()
	}
	parts := make([]string, 0, len(snap.Skills))
	for _, s := range snap.Skills {
		parts = append(parts, fmt.Sprintf("%d:%d", s.Line, s.Total()))
	}
	b.WriteString(strings.Join(parts, ","))
	return b.String()
}
