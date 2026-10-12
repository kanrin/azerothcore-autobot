package bot

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Point is a destination as it is written on a command line: "x,y,z" for the
// bot's own map, or "map:x,y,z" to name the map as well.
//
// It is parsed here, and not in the CLI, because it is pure logic that decides
// where a bot walks and therefore deserves a test that needs no server.
type Point struct {
	// MapID is only meaningful when HasMap is set: "x,y,z" means "wherever the
	// bot is", which is not known until it has logged in.
	MapID  uint32
	HasMap bool

	X, Y, Z float32
}

// ParsePoint parses "x,y,z" or "map:x,y,z". Spaces are ignored, so a coordinate
// copied out of a database query works as-is.
func ParsePoint(spec string) (Point, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Point{}, fmt.Errorf("bot: empty point")
	}

	var (
		point   Point
		coords  = trimmed
		mapPart string
	)
	if idx := strings.IndexByte(trimmed, ':'); idx >= 0 {
		mapPart, coords = trimmed[:idx], trimmed[idx+1:]
	}

	if mapPart != "" {
		mapID, err := strconv.ParseUint(strings.TrimSpace(mapPart), 10, 32)
		if err != nil {
			return Point{}, fmt.Errorf("bot: map id %q: %w", mapPart, err)
		}
		point.MapID, point.HasMap = uint32(mapID), true
	}

	parts := strings.Split(coords, ",")
	if len(parts) != 3 {
		return Point{}, fmt.Errorf("bot: point %q needs three coordinates, got %d", spec, len(parts))
	}

	values := make([]float32, 3)
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return Point{}, fmt.Errorf("bot: coordinate %d of %q: %w", i+1, spec, err)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Point{}, fmt.Errorf("bot: coordinate %d of %q is not a number", i+1, spec)
		}
		values[i] = float32(v)
	}
	point.X, point.Y, point.Z = values[0], values[1], values[2]
	return point, nil
}

// Destination resolves the point against the map the bot is on.
func (p Point) Destination(mapID uint32, reason string) Destination {
	if p.HasMap {
		mapID = p.MapID
	}
	return Destination{MapID: mapID, X: p.X, Y: p.Y, Z: p.Z, Reason: reason}
}

// String renders the point for logs.
func (p Point) String() string {
	if p.HasMap {
		return fmt.Sprintf("map %d (%.2f, %.2f, %.2f)", p.MapID, p.X, p.Y, p.Z)
	}
	return fmt.Sprintf("(%.2f, %.2f, %.2f)", p.X, p.Y, p.Z)
}
