//go:build integration

package itest

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

type chatMemberJSON struct {
	Loop    string `json:"loop"`
	Status  string `json:"status"`
	AddLink string `json:"add_link"`
}

type chatJSON struct {
	Surface string           `json:"surface"`
	ChatID  string           `json:"chat_id"`
	Title   string           `json:"title"`
	BoundAt int64            `json:"bound_at"`
	SeenAt  *int64           `json:"seen_at"`
	Members []chatMemberJSON `json:"members"`
}

type channelChatJSON struct {
	Chat     *chatJSON `json:"chat"`
	Surfaces []string  `json:"surfaces"`
}

func (srv *server) channelChat(name string) channelChatJSON {
	srv.t.Helper()
	var view channelChatJSON
	srv.mustJSON("GET", "/api/channels/"+name, nil, &view)
	return view
}

// memberStatus is a member loop's status under the channel's chat, "" for
// a loop the chat doesn't list.
func (view channelChatJSON) memberStatus(loop string) chatMemberJSON {
	if view.Chat == nil {
		return chatMemberJSON{}
	}
	for _, member := range view.Chat.Members {
		if member.Loop == loop {
			return member
		}
	}
	return chatMemberJSON{}
}

func chatBody(chatID int64) map[string]any {
	return map[string]any{"surface": "telegram", "chat_id": strconv.FormatInt(chatID, 10)}
}

// waitRoomChannel polls until the loop's room for roomID carries channel.
func (srv *server) waitRoomChannel(loop, roomID, channel string) {
	srv.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if room, ok := srv.room(loop, roomID); ok && room.Channel == channel {
			return
		}
	}
	srv.t.Fatalf("%s's room %s never carried %q: %+v", loop, roomID, channel, srv.rooms(loop))
}

// A chat is bound to a channel once, on the channel, for every member
// (ADR-0070): binding it binds each member's room already recorded for it,
// a member's room recorded later binds as it is heard, and a loop outside
// the channel binds nothing. A loop with no bot on the chat's surface
// can't be put in the channel, by the channel's put or by a team move;
// removing a member's token or taking a member out is never refused.
// Unbinding the chat unbinds every member's room, still recorded.
func TestAChannelsChatBindsItsMembers(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8282, First: "Operator", Username: "operator"}
	outside := map[string]any{"fleet": false}
	srv, tg := startTelegramFleet(t, operator, outside, outside)
	group := strconv.FormatInt(groupChatID, 10)
	srv.joinChannel("backend", "alpha")
	tg.addBot("gamma")
	srv.createLoop("gamma", map[string]any{"tg_bot_token": "gamma", "fleet": false})
	srv.joinChannel("backend", "gamma")

	if view := srv.channelChat("backend"); view.Chat != nil || strings.Join(view.Surfaces, ",") != "telegram" {
		t.Fatalf("backend before a bind = %+v, want no chat and telegram offered", view)
	}
	var chat chatJSON
	srv.mustJSON("PUT", "/api/channels/backend/chat", chatBody(groupChatID), &chat)
	if chat.ChatID != group || chat.SeenAt == nil {
		t.Errorf("bound chat = %+v, want the group, seen by alpha's bot", chat)
	}
	if room, _ := srv.room("alpha", group); room.Channel != "backend" {
		t.Errorf("alpha's room for the group = %+v, want it carrying backend", room)
	}
	if room, _ := srv.room("beta", group); room.Channel != "" {
		t.Errorf("beta, outside backend, carries %+v, want nothing", room)
	}
	view := srv.channelChat("backend")
	if got := view.memberStatus("alpha"); got.Status != "carrying" {
		t.Errorf("alpha's status = %+v, want carrying", got)
	}
	if got := view.memberStatus("gamma"); got.Status != "not_in_chat" || !strings.Contains(got.AddLink, "gamma_bot?startgroup=") {
		t.Errorf("gamma's status = %+v, want not_in_chat with its startgroup link", got)
	}

	// gamma's bot hears the group: its room binds with no second write
	tg.post(groupChatID, "supergroup", "gamma, welcome", operator)
	srv.waitRoomChannel("gamma", group, "backend")
	if got := srv.channelChat("backend").memberStatus("gamma"); got.Status != "carrying" {
		t.Errorf("gamma's status once its bot is in = %+v, want carrying", got)
	}

	// a loop with no Telegram bot can't be put in, by the channel's put or a team move
	srv.createLoop("web", outside)
	srv.wantRefusal("PUT", "/api/channels/backend/loops/web", nil, 409, "loop_not_on_surface")
	srv.mustJSON("POST", "/api/teams", map[string]any{"name": "red", "from_channel": "backend"}, nil)
	srv.wantRefusal("PATCH", "/api/loops/web", map[string]any{"team": "red"}, 409, "loop_not_on_surface")
	if got := srv.loop("web"); got.Team != "" {
		t.Errorf("the refused move left web in team %q", got.Team)
	}
	// a second chat is refused; a channel has one
	srv.wantRefusal("PUT", "/api/channels/backend/chat", chatBody(-1009990001), 409, "channel_has_chat")

	// removing a member's token and a member leaving are never refused
	srv.wantRefusal("PATCH", "/api/loops/gamma", map[string]any{"tg_bot_token": ""}, 200, "")
	if got := srv.channelChat("backend").memberStatus("gamma"); got.Status != "no_bot" {
		t.Errorf("gamma's status with no token = %+v, want no_bot", got)
	}
	srv.wantRefusal("DELETE", "/api/channels/backend/loops/gamma", nil, 204, "")

	srv.wantRefusal("DELETE", "/api/channels/backend/chat", nil, 204, "")
	if room, ok := srv.room("alpha", group); !ok || room.Channel != "" {
		t.Errorf("alpha's room after the unbind = %+v (recorded %v), want it recorded unbound", room, ok)
	}
	if view := srv.channelChat("backend"); view.Chat != nil {
		t.Errorf("backend after the unbind = %+v, want no chat", view)
	}
	srv.wantRefusal("DELETE", "/api/channels/backend/chat", nil, 404, "chat_not_found")

	// a home channel with no chat takes any loop, and then takes no chat on a
	// surface one of its members has no bot on
	srv.wantRefusal("PATCH", "/api/loops/web", map[string]any{"team": "red"}, 200, "")
	srv.wantRefusal("PUT", "/api/channels/backend/chat", chatBody(groupChatID), 409, "members_not_on_surface")
	if view := srv.channelChat("backend"); view.Chat != nil {
		t.Errorf("backend after the refused bind = %+v, want no chat", view)
	}
}

// A chat the channel's members' bots are in, that no channel has, is
// offered to confirm; once it is the channel's, it is offered no more.
func TestAChannelsFoundChats(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8383, First: "Operator", Username: "operator"}
	outside := map[string]any{"fleet": false}
	srv, _ := startTelegramFleet(t, operator, outside, outside)
	srv.joinChannel("backend", "alpha")
	srv.joinChannel("backend", "beta")
	var found []struct {
		ChatID  string   `json:"chat_id"`
		FoundBy []string `json:"found_by"`
	}
	srv.mustJSON("GET", "/api/channels/backend/chats/found", nil, &found)
	if len(found) != 1 || found[0].ChatID != strconv.FormatInt(groupChatID, 10) || strings.Join(found[0].FoundBy, ",") != "alpha,beta" {
		t.Fatalf("found = %+v, want the group, found by alpha and beta", found)
	}
	srv.mustJSON("PUT", "/api/channels/backend/chat", chatBody(groupChatID), nil)
	srv.mustJSON("GET", "/api/channels/backend/chats/found", nil, &found)
	if len(found) != 0 {
		t.Errorf("found once bound = %+v, want none", found)
	}
}

// A hub whose loops bound one channel to two chats upgrades with the
// earliest-bound as the channel's chat: its rooms stay bound, the other
// stays recorded unbound, the log names the channel, and the channel's
// page shows the left-out member. A member the upgrade left with no bot
// on the chat's surface keeps a team from being made from the channel.
func TestTheChannelChatUpgradeKeepsTheEarliestChat(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8484, First: "Operator", Username: "operator"}
	dataDir := t.TempDir()
	srv, _ := startTelegramFleetIn(t, dataDir, operator)
	srv.createLoop("web", map[string]any{"fleet": false})
	srv.stop()

	const earlier = "-1007770001"
	db, err := sql.Open("sqlite", "file:"+dataDir+"/spool.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE channel_chats`,
		`DELETE FROM schema_migrations WHERE version='0083_channel_chats.sql'`,
		// beta's room in another chat, bound to fleet before alpha's
		`DELETE FROM rooms WHERE loop_id = (SELECT id FROM loops WHERE name='beta')`,
		`INSERT INTO rooms (loop_id, surface, room_id, title, channel, first_seen_at, bound_at)
			SELECT id, 'telegram', '` + earlier + `', 'Earlier', 'fleet', 1, 1 FROM loops WHERE name='beta'`,
		// web, with no bot, was let in before channels had chats
		`INSERT INTO channel_loops (channel, loop_id, added_at) SELECT 'fleet', id, 1 FROM loops WHERE name='web'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()

	srv = startTelegramServer(t, dataDir, startFakeTelegram(t, "alpha", "beta"))
	view := srv.channelChat(fleetChannel)
	if view.Chat == nil || view.Chat.ChatID != earlier {
		t.Fatalf("fleet's chat after the upgrade = %+v, want %s, the earliest bound", view.Chat, earlier)
	}
	if room, _ := srv.room("beta", earlier); room.Channel != fleetChannel {
		t.Errorf("beta's room in the kept chat = %+v, want it still carrying fleet", room)
	}
	if room, ok := srv.room("alpha", strconv.FormatInt(groupChatID, 10)); !ok || room.Channel != "" {
		t.Errorf("alpha's room in the other chat = %+v (recorded %v), want it recorded unbound", room, ok)
	}
	for loop, want := range map[string]string{"alpha": "not_in_chat", "beta": "carrying", "web": "no_bot"} {
		if got := view.memberStatus(loop); got.Status != want {
			t.Errorf("%s's status after the upgrade = %+v, want %s", loop, got, want)
		}
	}
	if !srv.logged("channel chat upgrade") || !srv.logged("channel=fleet") {
		t.Errorf("the upgrade logged no line naming fleet:\n%s", srv.log())
	}
	srv.wantRefusal("POST", "/api/teams", map[string]any{"name": "fleet-team", "from_channel": fleetChannel}, 409, "loop_not_on_surface")
}
