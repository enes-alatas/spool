package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// withTelegramBot gives each loop a Telegram bot, the surface the chats in
// these tests are on.
func withTelegramBot(t *testing.T, db *DB, ids ...string) {
	t.Helper()
	token, username := "123:synthetic-token", "synthetic_bot"
	for _, id := range ids {
		if _, err := db.Loops().Edit(context.Background(), id, store.LoopEdit{TGBotToken: &token, TGBotUsername: &username}); err != nil {
			t.Fatal(err)
		}
	}
}

func sight(t *testing.T, db *DB, loopID, roomID, title string, at int64) {
	t.Helper()
	if _, _, err := db.Rooms().Sight(context.Background(), &store.Room{LoopID: loopID, Surface: store.SurfaceTelegram,
		RoomID: roomID, Title: title, FirstSeenAt: at}); err != nil {
		t.Fatal(err)
	}
}

func makeChannel(t *testing.T, db *DB, name string, loopIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if err := db.Channels().Create(ctx, &store.Channel{Name: name, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for _, loopID := range loopIDs {
		if err := db.Channels().AddLoop(ctx, name, loopID, 2); err != nil {
			t.Fatal(err)
		}
	}
}

func bindTelegramChat(db *DB, channel, chatID string, at int64) (*store.ChannelChat, error) {
	return db.Channels().BindChat(context.Background(), &store.ChannelChat{Channel: channel,
		Surface: store.SurfaceTelegram, ChatID: chatID, BoundAt: at, BoundBy: "admin"})
}

func channelOf(t *testing.T, db *DB, loopID, roomID string) string {
	t.Helper()
	room := roomOf(t, db, loopID, roomID)
	if room == nil {
		t.Fatalf("%s has no room %s", loopID, roomID)
	}
	return room.Channel
}

// Binding a chat binds every member's room recorded for it at once, and a
// member's room recorded later binds as it is recorded. A loop outside the
// channel binds nothing, and the chat takes the title and first sighting
// a member's bot gave it.
func TestBindingAChatBindsItsMembers(t *testing.T) {
	db := openTestDB(t)
	createLoops(t, db, "l1", "l2", "l3", "out")
	withTelegramBot(t, db, "l1", "l2", "l3", "out")
	makeChannel(t, db, "backend", "l1", "l2", "l3")
	sight(t, db, "l1", "-100", "Backend", 10)
	sight(t, db, "out", "-100", "Backend", 5)

	chat, err := bindTelegramChat(db, "backend", "-100", 20)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Title != "Backend" || chat.SeenAt != 10 || chat.BoundBy != "admin" {
		t.Errorf("chat = %+v, want the member's title and sighting, bound by admin", chat)
	}
	if got := channelOf(t, db, "l1", "-100"); got != "backend" {
		t.Errorf("l1's room carries %q, want backend", got)
	}
	if got := channelOf(t, db, "out", "-100"); got != "" {
		t.Errorf("a loop outside the channel carries %q, want nothing", got)
	}
	sight(t, db, "l2", "-100", "Backend", 30)
	if got := roomOf(t, db, "l2", "-100"); got.Channel != "backend" || got.BoundAt != 30 {
		t.Errorf("l2's room recorded after the bind is %+v, want it bound at 30", got)
	}
	// a member put in later binds the room its bot already had
	createLoops(t, db, "l4")
	withTelegramBot(t, db, "l4")
	sight(t, db, "l4", "-100", "Backend", 40)
	if got := channelOf(t, db, "l4", "-100"); got != "" {
		t.Fatalf("l4, outside the channel, carries %q", got)
	}
	if err := db.Channels().AddLoop(context.Background(), "backend", "l4", 50); err != nil {
		t.Fatal(err)
	}
	if got := channelOf(t, db, "l4", "-100"); got != "backend" {
		t.Errorf("l4 put in the channel carries %q, want backend", got)
	}
}

// A channel has one chat and a chat is one channel's; a chat on a surface
// a member has no bot on is refused, naming the member, with nothing
// changed.
func TestAChatIsBoundOnce(t *testing.T) {
	db := openTestDB(t)
	createLoops(t, db, "l1", "l2", "web")
	withTelegramBot(t, db, "l1", "l2")
	makeChannel(t, db, "backend", "l1")
	makeChannel(t, db, "frontend", "l2")
	makeChannel(t, db, "mixed", "l1", "web")
	if _, err := bindTelegramChat(db, "backend", "-100", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := bindTelegramChat(db, "backend", "-200", 2); !errors.Is(err, store.ErrChannelHasChat) {
		t.Errorf("a second chat = %v, want ErrChannelHasChat", err)
	}
	if _, err := bindTelegramChat(db, "frontend", "-100", 3); !errors.Is(err, store.ErrRoomInUse) {
		t.Errorf("another channel's chat = %v, want ErrRoomInUse", err)
	}
	if _, err := bindTelegramChat(db, "nowhere", "-300", 4); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no such channel = %v, want ErrNotFound", err)
	}
	_, err := bindTelegramChat(db, "mixed", "-400", 5)
	var refusal *store.NotOnSurfaceError
	if !errors.As(err, &refusal) || refusal.Surface != store.SurfaceTelegram || !slices.Equal(refusal.LoopIDs, []string{"web"}) {
		t.Fatalf("a member with no bot = %v, want a refusal naming web on telegram", err)
	}
	if _, err := db.Channels().Chat(context.Background(), "mixed"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused bind left a chat: %v", err)
	}
}

// While a channel has a chat, each write that would put a loop with no bot
// on its surface among the members is refused: the channel's own put, a
// team move into a team whose home it is, making it a team's home, and
// making a team from it. Taking a loop out, or its token, never is.
func TestAChatsChannelTakesOnlyLoopsOnItsSurface(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "web")
	withTelegramBot(t, db, "l1")
	makeChannel(t, db, "backend", "l1")
	if _, err := bindTelegramChat(db, "backend", "-100", 1); err != nil {
		t.Fatal(err)
	}
	isRefusal := func(err error) bool {
		var refusal *store.NotOnSurfaceError
		return errors.As(err, &refusal) && slices.Equal(refusal.LoopIDs, []string{"web"})
	}

	if err := db.Channels().AddLoop(ctx, "backend", "web", 2); !isRefusal(err) {
		t.Errorf("the channel's put = %v, want a refusal naming web", err)
	}
	// a team whose home is the channel refuses web too
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", CreatedAt: 3}); err != nil {
		t.Fatal(err)
	}
	red := "red"
	if _, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{Team: &red}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Loops().Edit(ctx, "web", store.LoopEdit{Team: &red}); err != nil {
		t.Fatal(err)
	}
	home := "backend"
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &home}); !isRefusal(err) {
		t.Errorf("making it red's home with web in red = %v, want a refusal naming web", err)
	}
	none := ""
	if _, err := db.Loops().Edit(ctx, "web", store.LoopEdit{Team: &none}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &home}); err != nil {
		t.Fatalf("making it red's home with l1 alone: %v", err)
	}
	if _, err := db.Loops().Edit(ctx, "web", store.LoopEdit{Team: &red}); !isRefusal(err) {
		t.Errorf("a team move into red = %v, want a refusal naming web", err)
	}
	if got, _ := db.Loops().Get(ctx, "web"); got.Team != "" {
		t.Errorf("the refused move left web in %q", got.Team)
	}

	// removing l1's token and taking it out are never refused
	empty := ""
	if _, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{TGBotToken: &empty, TGBotUsername: &empty}); err != nil {
		t.Errorf("removing a member's token: %v", err)
	}
	if err := db.Channels().RemoveLoop(ctx, "backend", "l1", 9); err != nil {
		t.Errorf("a member leaving: %v", err)
	}
}

// A team made from a channel whose chat a member has no bot on is refused.
func TestATeamFromAChatsChannelTakesOnlyLoopsOnItsSurface(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "web")
	withTelegramBot(t, db, "l1")
	makeChannel(t, db, "backend", "l1", "web")
	// the upgrade can leave a member off the chat's surface; this is how
	if _, err := db.db.Exec(`INSERT INTO channel_chats (channel, surface, chat_id, bound_at) VALUES ('backend','telegram','-100',1)`); err != nil {
		t.Fatal(err)
	}
	err := db.Teams().CreateFromChannel(ctx, &store.Team{Name: "backend-team", HomeChannel: "backend", CreatedAt: 2})
	var refusal *store.NotOnSurfaceError
	if !errors.As(err, &refusal) || !slices.Equal(refusal.LoopIDs, []string{"web"}) {
		t.Fatalf("a team from the channel = %v, want a refusal naming web", err)
	}
	if _, err := db.Teams().Get(ctx, "backend-team"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused create left a team: %v", err)
	}
}

// Unbinding a chat unbinds every member's room for it, still recorded,
// and a member's room the chat didn't bind waits for the operator's bind.
func TestUnbindingAChatAndBindingAMember(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	withTelegramBot(t, db, "l1", "l2")
	makeChannel(t, db, "backend", "l1", "l2")
	sight(t, db, "l1", "-100", "Backend", 1)
	if _, err := bindTelegramChat(db, "backend", "-100", 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Channels().BindMember(ctx, "backend", "l2", 3); !errors.Is(err, store.ErrNotInChat) {
		t.Errorf("binding l2 with no room = %v, want ErrNotInChat", err)
	}
	if err := db.Channels().UnbindChat(ctx, "backend"); err != nil {
		t.Fatal(err)
	}
	if got := roomOf(t, db, "l1", "-100"); got == nil || got.Channel != "" {
		t.Errorf("l1's room after the unbind is %+v, want it recorded unbound", got)
	}
	if err := db.Channels().UnbindChat(ctx, "backend"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unbinding twice = %v, want ErrNotFound", err)
	}
	// a room left unbound, as the upgrade leaves one, binds on the operator's word
	if _, err := db.db.Exec(`INSERT INTO channel_chats (channel, surface, chat_id, bound_at) VALUES ('backend','telegram','-100',4)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Channels().BindMember(ctx, "backend", "l1", 5); err != nil {
		t.Fatal(err)
	}
	if got := roomOf(t, db, "l1", "-100"); got.Channel != "backend" || got.BoundAt != 5 {
		t.Errorf("l1's room bound by the operator is %+v, want backend at 5", got)
	}
	if err := db.Channels().BindMember(ctx, "backend", "nobody", 6); !errors.Is(err, store.ErrNotInChannel) {
		t.Errorf("a loop not in the channel = %v, want ErrNotInChannel", err)
	}
}

// The chats offered for confirming are the members' unbound ones that are
// no channel's chat, on a surface every member has a bot on.
func TestFoundChats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2", "l3")
	withTelegramBot(t, db, "l1", "l2", "l3")
	makeChannel(t, db, "backend", "l1", "l2")
	makeChannel(t, db, "other", "l3")
	sight(t, db, "l1", "-100", "", 1)
	sight(t, db, "l2", "-100", "Backend", 2)
	sight(t, db, "l3", "-200", "Taken", 3)
	sight(t, db, "l1", "-200", "Taken", 4)
	if _, err := bindTelegramChat(db, "other", "-200", 5); err != nil {
		t.Fatal(err)
	}
	found, err := db.Channels().FoundChats(ctx, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ChatID != "-100" || found[0].Title != "Backend" || found[0].FirstSeenAt != 1 ||
		!slices.Equal(found[0].FoundBy, []string{"l1", "l2"}) {
		t.Fatalf("found = %+v, want -100 alone, titled, found by l1 and l2", found)
	}
	// a member with no bot on telegram takes the offer away
	createLoops(t, db, "web")
	if err := db.Channels().AddLoop(ctx, "backend", "web", 6); err != nil {
		t.Fatal(err)
	}
	if found, _ := db.Channels().FoundChats(ctx, "backend"); len(found) != 0 {
		t.Errorf("found with a member off telegram = %+v, want none", found)
	}
}

// A Telegram group upgraded to a supergroup takes its channel's chat to
// the new id with its rooms.
func TestAChatFollowsItsRoomsToANewID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	withTelegramBot(t, db, "l1")
	makeChannel(t, db, "backend", "l1")
	sight(t, db, "l1", "-100", "Backend", 1)
	if _, err := bindTelegramChat(db, "backend", "-100", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Move(ctx, store.SurfaceTelegram, "-100", "-1001"); err != nil {
		t.Fatal(err)
	}
	if chat, _ := db.Channels().Chat(ctx, "backend"); chat == nil || chat.ChatID != "-1001" {
		t.Errorf("chat after the upgrade = %+v, want -1001", chat)
	}
}

// Moving a chat takes it from one channel and binds it to another in one
// write: the loops that carry it together move it together (#1424).
func TestMoveChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	withTelegramBot(t, db, "l1", "l2")
	makeChannel(t, db, "old", "l1", "l2")
	makeChannel(t, db, "new", "l1", "l2")
	sight(t, db, "l1", "-100", "Backend", 1)
	sight(t, db, "l2", "-100", "Backend", 1)
	if _, err := bindTelegramChat(db, "old", "-100", 2); err != nil {
		t.Fatal(err)
	}
	moved, err := db.Channels().MoveChat(ctx, "old", &store.ChannelChat{Channel: "new", Surface: store.SurfaceTelegram, ChatID: "-100", BoundAt: 3})
	if err != nil || moved.Channel != "new" {
		t.Fatalf("move = %+v, %v", moved, err)
	}
	for _, loopID := range []string{"l1", "l2"} {
		if got := channelOf(t, db, loopID, "-100"); got != "new" {
			t.Errorf("%s carries %q, want new", loopID, got)
		}
	}
	if _, err := db.Channels().Chat(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old kept its chat: %v", err)
	}
}

// The upgrade gives each channel the chat of its earliest-bound room, keeps
// every room bound to that chat, unbinds those bound in another chat, still
// recorded, and binds no room that was unbound. A chat the rooms carry for
// two channels is kept for the one that bound it earlier, and the other
// keeps no chat, its rooms unbound. Its record of what it unbound is
// reported and dropped at the start.
func TestChannelChatsMigrationKeepsTheEarliestChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2", "l3", "l4")
	makeChannel(t, db, "backend", "l1", "l2", "l3")
	makeChannel(t, db, "ops", "l4")
	sight(t, db, "l4", "-200", "Second", 1)
	sight(t, db, "l1", "-100", "First", 1)
	sight(t, db, "l2", "-200", "Second", 1)
	sight(t, db, "l3", "-100", "First", 1)
	for _, bind := range []struct {
		loopID, roomID string
		at             int64
	}{{"l1", "-100", 10}, {"l2", "-200", 5}} {
		if _, err := db.Rooms().Bind(ctx, bind.loopID, store.SurfaceTelegram, bind.roomID, "backend", bind.at); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{`DROP TABLE channel_chats`, `DELETE FROM schema_migrations WHERE version='0083_channel_chats.sql'`,
		// rooms from before the store refused a chat to a second channel:
		// l4 carries backend's earliest chat for ops, bound later
		`UPDATE rooms SET channel='ops', bound_at=7 WHERE loop_id='l4' AND room_id='-200'`} {
		if _, err := db.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}
	chat, err := db.Channels().Chat(ctx, "backend")
	if err != nil || chat.ChatID != "-200" || chat.Title != "Second" || chat.BoundAt != 5 || chat.BoundBy != "upgrade" {
		t.Fatalf("migrated chat = %+v, %v; want -200, the earliest bound", chat, err)
	}
	if got := roomOf(t, db, "l2", "-200"); got.Channel != "backend" || got.BoundAt != 5 {
		t.Errorf("l2's room in the kept chat is %+v, want it bound as it was", got)
	}
	if got := roomOf(t, db, "l1", "-100"); got == nil || got.Channel != "" {
		t.Errorf("l1's room in the other chat is %+v, want it recorded unbound", got)
	}
	if got := channelOf(t, db, "l3", "-100"); got != "" {
		t.Errorf("l3's unbound room carries %q, want it still unbound", got)
	}
	if _, err := db.Channels().Chat(ctx, "ops"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ops's chat after the upgrade: %v, want none, its chat kept for backend", err)
	}
	if got := channelOf(t, db, "l4", "-200"); got != "" {
		t.Errorf("l4's room in backend's chat carries %q, want it unbound", got)
	}
	if left := upgradeRecordLeft(t, db); left != 0 {
		t.Errorf("the migration's record of what it unbound is left behind: %d", left)
	}
}

// upgradeRecordLeft counts the tables named for migration 0083's record of
// what it unbound, temporary or not.
func upgradeRecordLeft(t *testing.T, db *DB) int {
	t.Helper()
	var left int
	if err := db.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sqlite_master WHERE name='channel_chat_upgrade_unbound')
		+ (SELECT COUNT(*) FROM sqlite_temp_master WHERE name='channel_chat_upgrade_unbound')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	return left
}

// An upgrade note that fails leaves its record for the next start, and the
// hub starts anyway; at the next start it reports and drops it.
func TestAFailedUpgradeNoteRunsAgainAtTheNextStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// a record whose report can't be read: no channel column
	if _, err := db.db.Exec(`CREATE TABLE channel_chat_upgrade_unbound (loop_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("a failed note stopped the start: %v", err)
	}
	if left := upgradeRecordLeft(t, db); left != 1 {
		t.Fatalf("the failed note's record = %d tables, want it kept for the next start", left)
	}
	if _, err := db.db.Exec(`DROP TABLE channel_chat_upgrade_unbound`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TABLE channel_chat_upgrade_unbound (channel TEXT, loop_id TEXT, surface TEXT, room_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if left := upgradeRecordLeft(t, reopened); left != 0 {
		t.Errorf("the record after the next start = %d tables, want it reported and dropped", left)
	}
}

// A loop made in a team whose home channel has a chat, with no bot on its
// surface, is refused and leaves no loop behind.
func TestALoopMadeIntoAChatsTeamNeedsABotThere(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	withTelegramBot(t, db, "l1")
	makeChannel(t, db, "backend", "l1")
	if _, err := bindTelegramChat(db, "backend", "-100", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().CreateFromChannel(ctx, &store.Team{Name: "red", HomeChannel: "backend", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	web := testLoop()
	web.ID, web.Name, web.Team = "web", "loop-web", "red"
	err := db.Loops().Create(ctx, web)
	var refusal *store.NotOnSurfaceError
	if !errors.As(err, &refusal) || !slices.Equal(refusal.LoopIDs, []string{"web"}) {
		t.Fatalf("making web in red = %v, want a refusal naming web", err)
	}
	if _, err := db.Loops().Get(ctx, "web"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused create left a loop: %v", err)
	}
}
