package wowdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Quest world knowledge: the tables that answer "who has work, and can this bot
// take it".
//
// Column names in this world database are worth stating explicitly, because they do
// not match most documentation:
//
//	quest_template          the id column is `ID`, not `entry`
//	creature_queststarter   `id` is the creature *template entry*, `quest` the quest
//	                        id (the table keyed by guid, not entry)
//
// They were checked against the live database with information_schema rather than
// assumed, which is also how the `creature.id` (not `id1`/`id2`/`id3`) quirk was
// found. A wrong column name here fails loudly, but a wrong *join* silently returns
// the wrong quests.

// Quest is the part of a quest template a bot needs before it can decide.
type Quest struct {
	ID    uint32
	Title string

	// Level is QuestLevel, which may be negative for special quests.
	Level int32

	// MinLevel is the level the player must have.
	MinLevel uint8

	Flags uint32
}

// QuestsOfferedBy returns the quest ids a creature offers.
//
// creature_queststarter is the "starts this quest" half of the relation: a creature
// that only *ends* a quest (creature_questender) is not a giver, which is exactly the
// distinction a bot needs before walking up to anyone.
func (c *Client) QuestsOfferedBy(ctx context.Context, creatureEntry uint32) ([]uint32, error) {
	if creatureEntry == 0 {
		return nil, errors.New("wowdata: quests offered by: creature entry is zero")
	}

	rows, err := c.world.QueryContext(ctx,
		`SELECT quest FROM creature_queststarter WHERE id = ? ORDER BY quest`, creatureEntry)
	if err != nil {
		return nil, fmt.Errorf("wowdata: quests offered by creature %d: %w", creatureEntry, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("wowdata: scan quest of creature %d: %w", creatureEntry, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wowdata: read quests of creature %d: %w", creatureEntry, err)
	}
	return ids, nil
}

// QuestsByIDs returns the templates for the given quest ids, keyed by id.
//
// Ids that have no template are simply absent from the result: a quest referenced by
// a starter row but missing from quest_template is a server data problem, and the
// caller can report it better than this layer can ("the giver lists a quest that does
// not exist").
func (c *Client) QuestsByIDs(ctx context.Context, ids []uint32) (map[uint32]Quest, error) {
	out := make(map[uint32]Quest, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	// The ids come from the server's own data, never from user input, but they are
	// still bound as parameters rather than interpolated.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := c.world.QueryContext(ctx,
		`SELECT ID, LogTitle, QuestLevel, MinLevel, Flags FROM quest_template `+
			`WHERE ID IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("wowdata: quest templates for %v: %w", ids, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			quest Quest
			level sql.NullInt64
		)
		if err := rows.Scan(&quest.ID, &quest.Title, &level, &quest.MinLevel, &quest.Flags); err != nil {
			return nil, fmt.Errorf("wowdata: scan quest template: %w", err)
		}
		quest.Level = int32(level.Int64)
		out[quest.ID] = quest
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wowdata: read quest templates: %w", err)
	}
	return out, nil
}

// QuestByID returns one quest template.
func (c *Client) QuestByID(ctx context.Context, questID uint32) (Quest, bool, error) {
	if questID == 0 {
		return Quest{}, false, errors.New("wowdata: quest by id: quest id is zero")
	}

	quests, err := c.QuestsByIDs(ctx, []uint32{questID})
	if err != nil {
		return Quest{}, false, err
	}
	quest, found := quests[questID]
	return quest, found, nil
}
