package wowdata

import (
	"context"
	"errors"
	"fmt"
)

// QuestsEndedBy returns the quest ids a creature will accept a hand-in for.
//
// creature_questender is the other half of the quest relation and a genuinely different
// question from creature_queststarter: Deputy Willem starts five quests and ends two, and
// a bot that confuses the two talks to the wrong NPC — the server checks
// `object->hasInvolvedQuest` and returns without a packet when it does not hold
// (QuestHandler.cpp:488).
func (c *Client) QuestsEndedBy(ctx context.Context, creatureEntry uint32) ([]uint32, error) {
	if creatureEntry == 0 {
		return nil, errors.New("wowdata: quests ended by: creature entry is zero")
	}

	rows, err := c.world.QueryContext(ctx,
		`SELECT quest FROM creature_questender WHERE id = ? ORDER BY quest`, creatureEntry)
	if err != nil {
		return nil, fmt.Errorf("wowdata: quests ended by creature %d: %w", creatureEntry, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("wowdata: scan ended quest of creature %d: %w", creatureEntry, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wowdata: read ended quests of creature %d: %w", creatureEntry, err)
	}
	return ids, nil
}
