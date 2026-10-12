package quest

import (
	"strings"
	"testing"
)

func candidates() []Candidate {
	return []Candidate{
		{ID: 18, Title: "Brotherhood of Thieves", Level: 4, MinLevel: 2},
		{ID: 783, Title: "A Threat Within", Level: 1, MinLevel: 1},
		{ID: 5261, Title: "Eagan Peltskinner", Level: 2, MinLevel: 1},
	}
}

// TestChooseAcceptable covers the pick order: takeable, then lowest quest level,
// then lowest id. The live data behind it is Deputy Willem's list (783 level 1,
// 5261 level 2, 18 level 4 with MinLevel 2).
func TestChooseAcceptable(t *testing.T) {
	tests := []struct {
		name        string
		candidates  []Candidate
		playerLevel uint8
		wantID      uint32
		wantOK      bool
	}{
		{
			name:        "a level 1 bot takes the level 1 quest",
			candidates:  candidates(),
			playerLevel: 1,
			wantID:      783,
			wantOK:      true,
		},
		{
			name:        "a level 2 bot still prefers the lowest level, not the newest",
			candidates:  candidates(),
			playerLevel: 2,
			wantID:      783,
			wantOK:      true,
		},
		{
			name: "the quest already in the log is skipped",
			candidates: []Candidate{
				{ID: 783, Title: "A Threat Within", Level: 1, MinLevel: 1, InLog: true},
				{ID: 5261, Title: "Eagan Peltskinner", Level: 2, MinLevel: 1},
			},
			playerLevel: 1,
			wantID:      5261,
			wantOK:      true,
		},
		{
			name: "everything is too high level",
			candidates: []Candidate{
				{ID: 18, Title: "Brotherhood of Thieves", Level: 4, MinLevel: 2},
				{ID: 3903, Title: "Milly Osworth", Level: 4, MinLevel: 2},
			},
			playerLevel: 1,
			wantOK:      false,
		},
		{
			name:        "nothing offered",
			candidates:  nil,
			playerLevel: 5,
			wantOK:      false,
		},
		{
			name: "ties break on the lower id, so runs repeat",
			candidates: []Candidate{
				{ID: 900, Title: "later id", Level: 3, MinLevel: 1},
				{ID: 100, Title: "lower id", Level: 3, MinLevel: 1},
			},
			playerLevel: 3,
			wantID:      100,
			wantOK:      true,
		},
		{
			name: "a level 0 quest is takeable at level 1",
			candidates: []Candidate{
				{ID: 7, Title: "starter", Level: 0, MinLevel: 0},
			},
			playerLevel: 1,
			wantID:      7,
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ChooseAcceptable(tc.candidates, tc.playerLevel)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (chose %+v)", ok, tc.wantOK, got)
			}
			if ok && got.ID != tc.wantID {
				t.Errorf("chose quest %d, want %d", got.ID, tc.wantID)
			}
			if !ok && got != (Candidate{}) {
				t.Errorf("chose %+v while reporting nothing takeable", got)
			}
		})
	}
}

// TestTakeable pins the two ways a quest is out of reach.
func TestTakeable(t *testing.T) {
	level1 := Candidate{ID: 783, Level: 1, MinLevel: 1}
	if !level1.Takeable(1) {
		t.Error("a quest with MinLevel 1 must be takeable at level 1")
	}
	if level1.Takeable(0) {
		t.Error("a quest with MinLevel 1 must not be takeable at level 0")
	}

	inLog := Candidate{ID: 783, MinLevel: 1, InLog: true}
	if inLog.Takeable(60) {
		t.Error("a quest already in the log must not be takeable")
	}
}

// TestExplain covers the diagnostics: an operator reading the log has to be able to
// tell "the NPC has nothing" from "everything is too high level".
func TestExplain(t *testing.T) {
	skips := Explain(candidates(), 1)
	if len(skips) != 3 {
		t.Fatalf("Explain returned %d rows, want one per candidate", len(skips))
	}

	byID := map[uint32]string{}
	for _, s := range skips {
		byID[s.Candidate.ID] = s.Reason
	}
	if byID[783] != "chosen" {
		t.Errorf("quest 783 reason = %q, want chosen", byID[783])
	}
	if !strings.Contains(byID[18], "level") {
		t.Errorf("quest 18 reason = %q, want it to mention the level", byID[18])
	}
	if byID[5261] == "" || byID[5261] == "chosen" {
		t.Errorf("quest 5261 reason = %q, want a skip reason", byID[5261])
	}

	// With nothing takeable the reasons still explain why.
	skips = Explain(candidates(), 1)
	noChosen := 0
	for _, s := range skips {
		if s.Reason == "chosen" {
			noChosen++
		}
	}
	if noChosen != 1 {
		t.Errorf("Explain reported %d chosen, want exactly 1", noChosen)
	}

	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q", got)
	}
	if got := itoa(123); got != "123" {
		t.Errorf("itoa(123) = %q", got)
	}
	if got := itoa(-7); got != "-7" {
		t.Errorf("itoa(-7) = %q", got)
	}
}
