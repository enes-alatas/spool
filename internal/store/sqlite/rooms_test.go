package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func roomOf(t *testing.T, db *DB, loopID, roomID string) *store.Room {
	t.Helper()
	rows, err := db.Rooms().List(context.Background(), loopID)
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range rows {
		if room.RoomID == roomID {
			return room
		}
	}
	return nil
}

// A chat the bot hears from is recorded once, unbound, and keeps the last
// title it was heard under.
func TestSightRecordsAnUnboundRoom(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")

	room, added, err := db.Rooms().Sight(ctx, &store.Room{LoopID: "l1", Surface: store.SurfaceTelegram,
		RoomID: "-1001", Title: "Backend", FirstSeenAt: 10})
	if err != nil || !added {
		t.Fatalf("first sight: added=%v err=%v, want added", added, err)
	}
	if room.Channel != "" || room.BoundAt != 0 || room.Title != "Backend" || room.FirstSeenAt != 10 {
		t.Fatalf("first sight stored %+v, want an unbound room titled Backend", room)
	}
	for _, title := range []string{"Backend team", ""} {
		room, added, err = db.Rooms().Sight(ctx, &store.Room{LoopID: "l1", Surface: store.SurfaceTelegram,
			RoomID: "-1001", Title: title, FirstSeenAt: 20})
		if err != nil || added {
			t.Fatalf("sight again: added=%v err=%v, want the room already there", added, err)
		}
	}
	if room.Title != "Backend team" || room.FirstSeenAt != 10 {
		t.Fatalf("after renames: %+v, want the last title named and the first sighting kept", room)
	}
}

// Binding is per loop per channel: a second room for the same channel
// unbinds the first, a room moves between channels, and re-binding a room
// to the channel it carries keeps when it was bound, which the ingest
// election reads.
func TestBindKeepsOneRoomPerChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	if err := db.Channels().Create(ctx, &store.Channel{Name: "backend", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	first, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1001", "backend", 100)
	if err != nil {
		t.Fatal(err)
	}
	if first.Channel != "backend" || first.BoundAt != 100 || first.FirstSeenAt != 100 {
		t.Fatalf("bind by id stored %+v, want a room bound at 100", first)
	}
	if again, _ := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1001", "backend", 200); again.BoundAt != 100 {
		t.Fatalf("re-binding to the same channel moved bound_at to %d, want 100", again.BoundAt)
	}
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1002", "backend", 300); err != nil {
		t.Fatal(err)
	}
	if old := roomOf(t, db, "l1", "-1001"); old.Channel != "" || old.BoundAt != 0 {
		t.Fatalf("the room replaced for backend is %+v, want it unbound", old)
	}
	moved, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1002", store.FleetChannel, 400)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Channel != store.FleetChannel || moved.BoundAt != 400 {
		t.Fatalf("moved room is %+v, want group bound at 400", moved)
	}
	loopRecord, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if loopRecord.TGGroupChatID != -1002 || loopRecord.TGGroupBoundAt != 400 {
		t.Fatalf("loop reads group chat %d bound at %d, want the fleet channel's room",
			loopRecord.TGGroupChatID, loopRecord.TGGroupBoundAt)
	}
}

// A chat carries one channel: another loop may bind it to the same channel,
// never to a different one, or a person's message in it would belong to two.
func TestBindRefusesARoomCarryingAnotherChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if err := db.Channels().Create(ctx, &store.Channel{Name: "backend", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1001", "backend", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l2", store.SurfaceTelegram, "-1001", store.FleetChannel, 200); !errors.Is(err, store.ErrRoomInUse) {
		t.Fatalf("binding l1's backend room to group for l2: err=%v, want ErrRoomInUse", err)
	}
	if _, err := db.Rooms().Bind(ctx, "l2", store.SurfaceTelegram, "-1001", "backend", 200); err != nil {
		t.Fatalf("binding it to the channel it carries: %v", err)
	}
	both, err := db.Rooms().ListByRoom(ctx, store.SurfaceTelegram, "-1001")
	if err != nil || len(both) != 2 {
		t.Fatalf("ListByRoom = %d rows, err %v; want both loops", len(both), err)
	}
	if _, err := db.Rooms().Bind(ctx, "ghost", store.SurfaceTelegram, "-1001", "backend", 300); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding for a missing loop: err=%v, want ErrNotFound", err)
	}
}

// Leaving a channel, or its deletion, leaves the loop's room for it bound to
// nothing; the fleet channel's room survives leaving the fleet channel, as
// the group binding always did.
func TestLeavingAChannelUnbindsItsRoom(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	for _, name := range []string{"backend", "release"} {
		if err := db.Channels().Create(ctx, &store.Channel{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, bind := range []struct{ loop, room, channel string }{
		{"l1", "-1001", "backend"}, {"l2", "-1001", "backend"}, {"l1", "-1002", "release"}, {"l1", "-1003", store.FleetChannel},
	} {
		if err := db.Channels().AddLoop(ctx, bind.channel, bind.loop, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Rooms().Bind(ctx, bind.loop, store.SurfaceTelegram, bind.room, bind.channel, 5); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Channels().RemoveLoop(ctx, "backend", "l1", 10); err != nil {
		t.Fatal(err)
	}
	if room := roomOf(t, db, "l1", "-1001"); room.Channel != "" {
		t.Errorf("l1 left backend and its room still carries %q", room.Channel)
	}
	if room := roomOf(t, db, "l2", "-1001"); room.Channel != "backend" {
		t.Errorf("l1 leaving unbound l2's room too: %q", room.Channel)
	}
	if err := db.Channels().Delete(ctx, "release"); err != nil {
		t.Fatal(err)
	}
	if room := roomOf(t, db, "l1", "-1002"); room.Channel != "" {
		t.Errorf("release was deleted and its room still carries %q", room.Channel)
	}
	if err := db.Channels().RemoveLoop(ctx, store.FleetChannel, "l1", 20); err != nil {
		t.Fatal(err)
	}
	if room := roomOf(t, db, "l1", "-1003"); room.Channel != store.FleetChannel {
		t.Errorf("leaving the fleet channel unbound its room: %q", room.Channel)
	}
}

// A chat's new id takes over every loop's room for the old one, bound as it
// was and since when: an unbound sighting of the new id gives way, and a
// loop that bound the new id already keeps that row. A second move finds
// nothing left, and a move into a chat carrying another channel is refused.
func TestMoveCarriesARoomToTheChatsNewID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2", "l3", "l4")
	for _, loopID := range []string{"l1", "l2", "l3"} {
		if _, err := db.Rooms().Bind(ctx, loopID, store.SurfaceTelegram, "-1", store.FleetChannel, 5); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := db.Rooms().Sight(ctx, &store.Room{LoopID: "l2", Surface: store.SurfaceTelegram,
		RoomID: "-1001", FirstSeenAt: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l3", store.SurfaceTelegram, "-1001", store.FleetChannel, 9); err != nil {
		t.Fatal(err)
	}

	moved, err := db.Rooms().Move(ctx, store.SurfaceTelegram, "-1", "-1001")
	if err != nil || len(moved) != 2 || moved[0] != "l1" || moved[1] != "l2" {
		t.Fatalf("Move = %v, %v; want l1 and l2 moved", moved, err)
	}
	for _, loopID := range []string{"l1", "l2"} {
		if room := roomOf(t, db, loopID, "-1001"); room == nil || room.Channel != store.FleetChannel || room.BoundAt != 5 {
			t.Errorf("%s's room after the move = %+v, want the fleet channel bound since 5", loopID, room)
		}
		if room := roomOf(t, db, loopID, "-1"); room != nil {
			t.Errorf("%s still has the old id: %+v", loopID, room)
		}
	}
	if room := roomOf(t, db, "l3", "-1001"); room == nil || room.BoundAt != 9 {
		t.Errorf("l3's own binding of the new id = %+v, want it kept", room)
	}
	if moved, err := db.Rooms().Move(ctx, store.SurfaceTelegram, "-1", "-1001"); err != nil || len(moved) != 0 {
		t.Errorf("a second move = %v, %v; want nothing moved", moved, err)
	}

	if _, err := db.Rooms().Bind(ctx, "l4", store.SurfaceTelegram, "-1002", "backend", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Move(ctx, store.SurfaceTelegram, "-1001", "-1002"); !errors.Is(err, store.ErrRoomInUse) {
		t.Errorf("a move into a chat carrying backend: err=%v, want ErrRoomInUse", err)
	}
}

// Clearing a loop's bot token forgets every Telegram room it knew, bound or
// not, and Forget removes one.
func TestRoomsGoWithTheBot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1001", store.FleetChannel, 5); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"-1002", "-1003"} {
		if _, _, err := db.Rooms().Sight(ctx, &store.Room{LoopID: "l1", Surface: store.SurfaceTelegram, RoomID: id, FirstSeenAt: 6}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Rooms().Forget(ctx, "l1", store.SurfaceTelegram, "-1003"); err != nil {
		t.Fatal(err)
	}
	if err := db.Rooms().Forget(ctx, "l1", store.SurfaceTelegram, "-1003"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("forgetting twice: err=%v, want ErrNotFound", err)
	}
	empty := ""
	if _, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{TGBotToken: &empty, TGBotUsername: &empty,
		ClearTelegramRooms: true, UpdatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	left, err := db.Rooms().List(ctx, "l1")
	if err != nil || len(left) != 0 {
		t.Fatalf("after the token was cleared: %d rooms, err %v; want none", len(left), err)
	}
}

// unmigrate0034 returns the database to its shape before rooms, the fleet
// channel's group back in the loops columns it was read from.
func unmigrate0034(db *DB) error {
	for _, stmt := range []string{
		`ALTER TABLE loops ADD COLUMN tg_group_chat_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE loops ADD COLUMN tg_group_bound_at INTEGER NOT NULL DEFAULT 0`,
		`UPDATE loops SET
			tg_group_chat_id = COALESCE((SELECT CAST(room_id AS INTEGER) FROM rooms WHERE loop_id=loops.id AND channel='group'), 0),
			tg_group_bound_at = COALESCE((SELECT bound_at FROM rooms WHERE loop_id=loops.id AND channel='group'), 0)`,
		`DROP TABLE rooms`,
		`DELETE FROM schema_migrations WHERE version='0034_rooms.sql'`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// An existing fleet upgrades with every loop's group as its fleet channel's
// room, bound when it was, so the ingest election reads the same answer.
func TestRoomsMigrationKeepsTheGroup(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-1001234567890", store.FleetChannel, 77); err != nil {
		t.Fatal(err)
	}
	if err := unmigrate0034(db); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]int64{"l1": {-1001234567890, 77}, "l2": {0, 0}} {
		loopRecord, err := db.Loops().Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := [2]int64{loopRecord.TGGroupChatID, loopRecord.TGGroupBoundAt}; got != want {
			t.Errorf("%s migrated to group %v, want %v", id, got, want)
		}
	}
	if left, _ := db.Rooms().List(ctx, "l2"); len(left) != 0 {
		t.Errorf("an unbound loop migrated into %d rooms, want none", len(left))
	}
}

// unmigrate0043 returns the database to its shape before Slack rooms, the
// fleet channel's Slack channel back in the loops columns it was read from.
func unmigrate0043(db *DB) error {
	for _, stmt := range []string{
		`ALTER TABLE loops ADD COLUMN slack_channel_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE loops ADD COLUMN slack_channel_bound_at INTEGER NOT NULL DEFAULT 0`,
		`UPDATE loops SET
			slack_channel_id = COALESCE((SELECT room_id FROM rooms WHERE loop_id=loops.id AND surface='slack' AND channel='group'), ''),
			slack_channel_bound_at = COALESCE((SELECT bound_at FROM rooms WHERE loop_id=loops.id AND surface='slack' AND channel='group'), 0)`,
		`DELETE FROM rooms WHERE surface='slack'`,
		`DELETE FROM schema_migrations WHERE version='0043_slack_rooms.sql'`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// An existing fleet upgrades with every loop's Slack channel as its fleet
// channel's room on Slack, bound when it was, and its Telegram room as it
// was (#548).
func TestRoomsMigrationKeepsTheSlackChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceSlack, "C0FLEET", store.FleetChannel, 77); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l2", store.SurfaceTelegram, "-1001234567890", store.FleetChannel, 78); err != nil {
		t.Fatal(err)
	}
	if err := unmigrate0043(db); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct {
		channel string
		at      int64
		group   int64
	}{"l1": {"C0FLEET", 77, 0}, "l2": {"", 0, -1001234567890}} {
		loopRecord, err := db.Loops().Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if loopRecord.SlackChannelID != want.channel || loopRecord.SlackChannelBoundAt != want.at || loopRecord.TGGroupChatID != want.group {
			t.Errorf("%s migrated to slack %q at %d, group %d; want %+v", id,
				loopRecord.SlackChannelID, loopRecord.SlackChannelBoundAt, loopRecord.TGGroupChatID, want)
		}
	}
}
