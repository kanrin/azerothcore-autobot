package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"azerothcore-autobot/internal/wow"
	"azerothcore-autobot/internal/wow/state"
	"azerothcore-autobot/internal/wowdata"
)

// runState logs in and dumps the bot's own state: money, inventory and skills.
//
// It is the live evidence for internal/wow/state. Everything in that package is
// derived from update fields whose offsets come from AzerothCore's
// UpdateFields.h, and a wrong offset does not fail — it returns a plausible
// wrong number. This command is what turns that into something checkable: run it
// and compare the money, the item entries and the profession values against
// acore_characters (characters.money, item_instance, character_skills).
//
// The snapshot is read after waiting for the character's update values to reach
// the object cache; a timeout is reported as a warning rather than a failure,
// because what the command prints is diagnostic either way.
func runState(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := newBaseFlags(fs)
	asJSON := fs.Bool("json", false, "以 JSON 输出快照，便于与数据库比对")
	settle := fs.Duration("settle", 20*time.Second, "等待角色更新字段进入对象缓存的上限")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(base)
	if err != nil {
		return err
	}
	if err := requireAccountName(cfg); err != nil {
		return err
	}
	if err := requireCharName(cfg); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), base.timeout)
	defer cancel()

	data, err := wowdata.Open(dataOptions(cfg, logger))
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()

	if _, err := data.EnsureAccount(ctx, cfg.Account.Username, cfg.Account.Password); err != nil {
		return err
	}

	sess, err := wow.Dial(ctx, connectOptions(cfg, logger))
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	defer sess.Close()

	sess.SetCharRace(cfg.Character.Race)

	char, err := sess.EnterWorld(ctx, wow.EnterWorldOptions{
		Name:       cfg.Character.Name,
		Race:       cfg.Character.Race,
		Class:      cfg.Character.Class,
		Appearance: wow.DefaultAppearance(cfg.Character.Gender),
		Logf:       logf(logger),
	})
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}

	// The self object and its inventory arrive in their own SMSG_UPDATE_OBJECT
	// after SMSG_LOGIN_VERIFY_WORLD, so a snapshot taken right after EnterWorld
	// is legitimately empty. Wait for the values instead of reading a zero
	// snapshot and calling it "empty bags".
	err = sess.WaitFor(ctx, *settle, "character update fields in the object cache", func() bool {
		return snapshotHasData(state.Read(sess))
	})
	if err != nil {
		logger.Warn("state: cache still thin, printing what arrived", "err", err.Error())
	}

	snap := state.Read(sess)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			return fmt.Errorf("state: encode snapshot: %w", err)
		}
	} else {
		printSnapshot(os.Stdout, char.Name, char.GUID, snap)
	}

	logger.Info("state: OK", "character", char.Name, "guid", char.GUID,
		"money", snap.Money, "items", len(snap.Items), "skills", len(snap.Skills),
		"unresolved", len(snap.UnknownEntries()))

	if err := sess.Logout(ctx, 0); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}

// snapshotHasData reports whether the update values have arrived: money, an item
// slot or a skill is enough to tell "not delivered yet" from "nothing to show".
func snapshotHasData(snap state.Snapshot) bool {
	return snap.Money > 0 || len(snap.Items) > 0 || len(snap.Skills) > 0
}

// printSnapshot writes the human-readable form of a snapshot to w.
func printSnapshot(w io.Writer, name string, guid uint64, snap state.Snapshot) {
	fmt.Fprintf(w, "character %s (guid %d)\n", name, guid)
	fmt.Fprintf(w, "money     %d copper\n", snap.Money)
	fmt.Fprintf(w, "bags      %d free slot(s)\n", snap.FreeBagSlots())

	fmt.Fprintf(w, "items     %d\n", len(snap.Items))
	for _, item := range snap.Items {
		if !item.Known {
			fmt.Fprintf(w, "  %-18s guid=%d (item not in the cache yet)\n", item.Slot, item.GUID)
			continue
		}
		fmt.Fprintf(w, "  %-18s guid=%d entry=%d count=%d durability=%d/%d flags=0x%04X\n",
			item.Slot, item.GUID, item.Entry, item.Count, item.Durability, item.MaxDurability, item.Flags)
	}

	fmt.Fprintf(w, "skills    %d\n", len(snap.Skills))
	for _, skill := range snap.Skills {
		fmt.Fprintf(w, "  line=%-4d step=%d value=%d max=%d bonus=%+d/%+d total=%d\n",
			skill.Line, skill.Step, skill.Value, skill.Max, skill.TempBonus, skill.PermBonus, skill.Total())
	}
}
