package quest

import "testing"

// TestChooseTurnIn covers the hand-in decision with the live data behind it: Deputy
// Willem ends quests 6 and 18 (creature_questender), so a completed 18 in the log is
// what a bot standing next to him should hand in.
func TestChooseTurnIn(t *testing.T) {
	willem := map[uint32]bool{6: true, 18: true}

	tests := []struct {
		name    string
		log     []LogEntry
		enders  map[uint32]bool
		wantID  uint32
		wantOK  bool
		comment string
	}{
		{
			name:   "a completed quest this npc ends",
			log:    []LogEntry{{ID: 18, Complete: true}},
			enders: willem,
			wantID: 18,
			wantOK: true,
		},
		{
			name:   "an incomplete quest is not handed in",
			log:    []LogEntry{{ID: 18, Complete: false}},
			enders: willem,
			wantOK: false,
		},
		{
			name:   "a completed quest this npc only starts",
			log:    []LogEntry{{ID: 783, Complete: true}},
			enders: willem,
			wantOK: false,
		},
		{
			name:   "nothing in the log",
			log:    nil,
			enders: willem,
			wantOK: false,
		},
		{
			name:   "npc that ends nothing",
			log:    []LogEntry{{ID: 18, Complete: true}},
			enders: map[uint32]bool{},
			wantOK: false,
		},
		{
			name:   "two candidates, the lower id wins so runs repeat",
			log:    []LogEntry{{ID: 18, Complete: true}, {ID: 6, Complete: true}},
			enders: willem,
			wantID: 6,
			wantOK: true,
		},
		{
			name:   "an incomplete quest does not block a complete one",
			log:    []LogEntry{{ID: 6, Complete: false}, {ID: 18, Complete: true}},
			enders: willem,
			wantID: 18,
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ChooseTurnIn(tc.log, tc.enders)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (chose %d)", ok, tc.wantOK, got)
			}
			if ok && got != tc.wantID {
				t.Errorf("chose %d, want %d", got, tc.wantID)
			}
		})
	}
}

// TestExplainTurnIn keeps the two ways of not handing something in distinguishable in
// the log.
func TestExplainTurnIn(t *testing.T) {
	willem := map[uint32]bool{6: true, 18: true}
	log := []LogEntry{
		{ID: 6, Complete: true},
		{ID: 18, Complete: false},
		{ID: 783, Complete: true},
	}

	reasons := map[uint32]string{}
	for _, row := range ExplainTurnIn(log, willem) {
		reasons[row.Candidate.ID] = row.Reason
	}
	if len(reasons) != len(log) {
		t.Fatalf("ExplainTurnIn returned %d rows for %d entries", len(reasons), len(log))
	}
	if reasons[6] != "chosen" {
		t.Errorf("quest 6 = %q, want chosen", reasons[6])
	}
	if reasons[18] != "objectives not done yet" {
		t.Errorf("quest 18 = %q", reasons[18])
	}
	if reasons[783] != "this npc does not end that quest" {
		t.Errorf("quest 783 = %q", reasons[783])
	}
}
