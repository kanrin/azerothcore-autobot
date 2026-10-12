package bot

import (
	"strings"
	"testing"
)

// TestParsePoint covers the two forms a destination is written in, and the ways
// a typo must fail: a silent default here would send a bot to the wrong map.
func TestParsePoint(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    Point
		wantErr string
	}{
		{
			name: "coordinates on the bot's own map",
			spec: "-8902.59,-162.606,82.0223",
			want: Point{X: -8902.59, Y: -162.606, Z: 82.0223},
		},
		{
			name: "an explicit map",
			spec: "0:-8902.59,-162.606,82.0223",
			want: Point{MapID: 0, HasMap: true, X: -8902.59, Y: -162.606, Z: 82.0223},
		},
		{
			name: "a non-zero map",
			spec: "1:1000,-4000,90",
			want: Point{MapID: 1, HasMap: true, X: 1000, Y: -4000, Z: 90},
		},
		{
			name: "spaces are ignored, so a copied row works",
			spec: "  0 : -8902.59 , -162.606 , 82.0223  ",
			want: Point{MapID: 0, HasMap: true, X: -8902.59, Y: -162.606, Z: 82.0223},
		},
		{name: "empty", spec: "   ", wantErr: "empty point"},
		{name: "two coordinates", spec: "-8902.59,-162.606", wantErr: "three coordinates"},
		{name: "four coordinates", spec: "1,2,3,4", wantErr: "three coordinates"},
		{name: "a word instead of a number", spec: "-8902.59,-162.606,east", wantErr: "coordinate 3"},
		{name: "a bad map id", spec: "west:1,2,3", wantErr: "map id"},
		{name: "nan", spec: "nan,2,3", wantErr: "not a number"},
		{name: "infinity", spec: "inf,2,3", wantErr: "not a number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePoint(tc.spec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParsePoint(%q) = %+v, want an error", tc.spec, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParsePoint(%q) error = %v, want it to mention %q", tc.spec, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePoint(%q): %v", tc.spec, err)
			}
			if got != tc.want {
				t.Errorf("ParsePoint(%q) = %+v, want %+v", tc.spec, got, tc.want)
			}
		})
	}
}

// TestPointDestination resolves a point against the bot's current map.
func TestPointDestination(t *testing.T) {
	own := Point{X: 1, Y: 2, Z: 3}
	if got := own.Destination(530, "quest"); got.MapID != 530 || got.Reason != "quest" {
		t.Errorf("Destination on the bot's map = %+v, want map 530", got)
	}

	explicit := Point{MapID: 1, HasMap: true, X: 1, Y: 2, Z: 3}
	if got := explicit.Destination(530, "quest"); got.MapID != 1 {
		t.Errorf("Destination with an explicit map = %+v, want map 1", got)
	}
}

// TestPointString keeps the log line readable: a destination is what an operator
// reads when a bot walks somewhere unexpected.
func TestPointString(t *testing.T) {
	if got := (Point{X: -8902.59, Y: -162.606, Z: 82.0223}).String(); !strings.Contains(got, "-8902.59") {
		t.Errorf("String = %q, want it to contain the coordinates", got)
	}
	if got := (Point{MapID: 0, HasMap: true, X: 1, Y: 2, Z: 3}).String(); !strings.Contains(got, "map 0") {
		t.Errorf("String = %q, want it to name the map", got)
	}
}
