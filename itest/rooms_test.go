//go:build integration

package itest

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

type roomJSON struct {
	Surface     string `json:"surface"`
	RoomID      string `json:"room_id"`
	Title       string `json:"title"`
	Channel     string `json:"channel"`
	FirstSeenAt int64  `json:"first_seen_at"`
	BoundAt     int64  `json:"bound_at"`
}

func (s *server) rooms(loop string) []roomJSON {
	s.t.Helper()
	var rooms []roomJSON
	s.mustJSON("GET", "/api/loops/"+loop+"/rooms", nil, &rooms)
	return rooms
}

func (s *server) room(loop, roomID string) (roomJSON, bool) {
	s.t.Helper()
	for _, room := range s.rooms(loop) {
		if room.RoomID == roomID {
			return room, true
		}
	}
	return roomJSON{}, false
}

func roomBody(roomID int64, channel string) map[string]any {
	return map[string]any{"surface": "telegram", "room_id": strconv.FormatInt(roomID, 10), "channel": channel}
}

// The operator binds a loop's rooms by id, one per channel, only to a
// channel the loop is in and only for a loop with a bot to sit there; a
// room carries one channel across loops; leaving a channel unbinds its
// room, and a forgotten room is gone (ADR-0038).
func TestRoomsRoundTrip(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6161, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)
	srv.createLoop("gamma", nil) // no bot
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	fleet, ok := srv.room("alpha", strconv.FormatInt(groupChatID, 10))
	if !ok || fleet.Channel != "group" || fleet.Surface != "telegram" || fleet.BoundAt == 0 {
		t.Fatalf("alpha's rooms = %+v, want the fleet channel's group bound", srv.rooms("alpha"))
	}

	const backendRoom int64 = -1009000000001
	var bound roomJSON
	srv.mustJSON("PUT", "/api/loops/alpha/rooms", roomBody(backendRoom, "backend"), &bound)
	if bound.Channel != "backend" || bound.Title != "" || bound.BoundAt == 0 {
		t.Fatalf("bound by id = %+v, want an untitled room bound to backend", bound)
	}
	srv.wantRefusal("PUT", "/api/loops/beta/rooms", roomBody(backendRoom, "group"), 409, "room_in_use")
	srv.wantRefusal("PUT", "/api/loops/beta/rooms", roomBody(backendRoom, "backend"), 200, "")
	srv.wantRefusal("PUT", "/api/loops/alpha/rooms", roomBody(-1009000000002, "release"), 400, "channel_not_found")
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "release"}, nil)
	srv.wantRefusal("PUT", "/api/loops/alpha/rooms", roomBody(-1009000000002, "release"), 409, "not_in_channel")
	srv.wantRefusal("PUT", "/api/loops/gamma/rooms", roomBody(-1009000000002, "backend"), 409, "no_bot")
	for _, body := range []map[string]any{
		{"surface": "slack", "room_id": "C123", "channel": "backend"},
		{"surface": "telegram", "room_id": "42", "channel": "backend"},
		{"surface": "telegram", "room_id": "group", "channel": "backend"},
	} {
		code := "room_id_invalid"
		if body["surface"] == "slack" {
			code = "room_surface_unsupported"
		}
		srv.wantRefusal("PUT", "/api/loops/alpha/rooms", body, 400, code)
	}
	srv.wantRefusal("PUT", "/api/loops/nobody/rooms", roomBody(backendRoom, "backend"), 404, "")

	// A second room for backend replaces the first, which stays listed unbound.
	const otherRoom int64 = -1009000000003
	srv.mustJSON("PUT", "/api/loops/alpha/rooms", roomBody(otherRoom, "backend"), nil)
	if old, _ := srv.room("alpha", strconv.FormatInt(backendRoom, 10)); old.Channel != "" {
		t.Fatalf("the replaced room = %+v, want it unbound", old)
	}

	srv.wantRefusal("DELETE", "/api/channels/backend/loops/beta", nil, 204, "")
	if left, _ := srv.room("beta", strconv.FormatInt(backendRoom, 10)); left.Channel != "" {
		t.Fatalf("beta left backend and its room = %+v, want it unbound", left)
	}

	path := "/api/loops/alpha/rooms/telegram/" + strconv.FormatInt(backendRoom, 10)
	srv.wantRefusal("DELETE", path, nil, 204, "")
	srv.wantRefusal("DELETE", path, nil, 404, "room_not_found")
	if _, ok := srv.room("alpha", strconv.FormatInt(backendRoom, 10)); ok {
		t.Fatal("a forgotten room is still listed")
	}
}

// nameChat gives a chat the title Telegram reports for it.
func (tg *fakeTelegram) nameChat(chatID int64, title string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.titles == nil {
		tg.titles = map[int64]string{}
	}
	tg.titles[chatID] = title
}

// A second Telegram group the bots hear from is recorded unbound and
// ingests nothing, the fleet channel's group staying where it was. Once the
// operator binds it to a channel, the channel's posts go there and not to
// the fleet room, and a person's message there arrives as the channel's,
// stored once and addressed among its loops (ADR-0038).
func TestARoomCarriesItsChannel(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7171, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	const backendChat int64 = -1009000000010
	backendID := strconv.FormatInt(backendChat, 10)
	tg.nameChat(backendChat, "Backend")
	const unbound = "@alpha before the room is bound"
	tg.post(backendChat, "supergroup", unbound, operator)
	deadline := time.Now().Add(20 * time.Second)
	for _, name := range []string{"alpha", "beta"} {
		for {
			if room, ok := srv.room(name, backendID); ok {
				if room.Channel != "" || room.Title != "Backend" {
					t.Fatalf("%s heard the new group as %+v, want it unbound and titled", name, room)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never recorded the new group: %+v", name, srv.rooms(name))
			}
			time.Sleep(200 * time.Millisecond)
		}
		if loop := srv.loop(name); loop.TGGroupChatID != groupChatID {
			t.Fatalf("a second group moved %s's fleet room to %d", name, loop.TGGroupChatID)
		}
	}
	if got := srv.activityWith(unbound); len(got) != 0 {
		t.Fatalf("a message in an unbound room was ingested: %s", dump(got))
	}

	for _, name := range []string{"alpha", "beta"} {
		srv.mustJSON("PUT", "/api/loops/"+name+"/rooms", roomBody(backendChat, "backend"), nil)
	}
	settleBindings()

	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	for _, send := range []map[string]any{
		{"destination": "channel:backend", "text": "@beta backend words"},
		{"destination": "group", "text": "@beta fleet words"},
	} {
		if res := callSend(t, alpha, send); res.IsError {
			t.Fatalf("send %v refused: %s", send, resultText(res))
		}
	}
	tg.waitSentFrom(t, backendChat, "alpha", "@beta backend words")
	tg.waitSentFrom(t, groupChatID, "alpha", "@beta fleet words")
	for _, sent := range tg.sentTo(groupChatID) {
		if strings.Contains(sent.Text, "backend words") {
			t.Fatalf("the channel's post reached the fleet room: %+v", sent)
		}
	}

	const inbound = "@alpha from the backend room"
	tg.post(backendChat, "supergroup", inbound, operator)
	srv.waitForMessage(inbound)
	srv.waitTurn("alpha", 20*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.Contains(tn.ResultText, "from the backend room")
	})
	time.Sleep(2 * time.Second) // let a second ingester add its row, if there were one
	stored := srv.activityWith(inbound)
	if len(stored) != 1 || stored[0].Channel != "backend" || stored[0].Conversation != "group" {
		t.Fatalf("the room's message was stored as %s, want once, in backend", dump(stored))
	}
	var header string
	for _, inputs := range srv.turnInputs("alpha") {
		for _, input := range inputs {
			if strings.Contains(input, "from the backend room") {
				header = input
			}
		}
	}
	if !strings.Contains(header, "· channel:backend · ref:") {
		t.Errorf("alpha's envelope does not name the channel:\n%s\n%s", header, dump(srv.turnInputs("alpha")))
	}

	// A reply threads under the message in the room it was said in.
	reply := map[string]any{"destination": "channel:backend", "text": "@beta threaded answer",
		"reply_to": "ref:" + strconv.FormatInt(stored[0].ID, 10)}
	if res := callSend(t, alpha, reply); res.IsError {
		t.Fatalf("reply refused: %s", resultText(res))
	}
	if sent := tg.waitSentFrom(t, backendChat, "alpha", "threaded answer"); sent.ReplyTo == 0 {
		t.Errorf("the reply was posted unthreaded: %+v", sent)
	}
}

// upgrade turns a basic group into a supergroup under a new chat id, as
// Telegram does: every bot hears a service message in each chat, the old
// one naming the new id and the new one naming the old.
func (tg *fakeTelegram) upgrade(oldChatID, newChatID int64, from user) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	for _, token := range tg.bots {
		for _, msg := range []map[string]any{
			{"chat": map[string]any{"id": oldChatID, "type": "group"}, "migrate_to_chat_id": newChatID},
			{"chat": map[string]any{"id": newChatID, "type": "supergroup"}, "migrate_from_chat_id": oldChatID},
		} {
			tg.nextID[token]++
			tg.updates++
			msg["message_id"] = tg.nextID[token]
			msg["date"] = time.Now().Unix()
			msg["from"] = map[string]any{"id": from.ID, "is_bot": false, "first_name": from.First, "username": from.Username}
			tg.queued[token] = append(tg.queued[token], map[string]any{"update_id": tg.updates, "message": msg})
		}
	}
}

// A group upgraded to a supergroup takes its rooms to the new chat id, bound
// as they were: the fleet channel hears the new chat and posts there,
// with no operator step (ADR-0038).
func TestAnUpgradedGroupKeepsItsRooms(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8181, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	before, _ := srv.room("alpha", strconv.FormatInt(groupChatID, 10))

	const upgraded int64 = -1009000000020
	tg.upgrade(groupChatID, upgraded, operator)
	deadline := time.Now().Add(20 * time.Second)
	for _, name := range []string{"alpha", "beta"} {
		for srv.loop(name).TGGroupChatID != upgraded {
			if time.Now().After(deadline) {
				t.Fatalf("%s's rooms after the upgrade: %+v", name, srv.rooms(name))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if after, _ := srv.room("alpha", strconv.FormatInt(upgraded, 10)); after.Channel != "group" || after.BoundAt != before.BoundAt {
		t.Fatalf("alpha's room moved as %+v, want it bound to group since %d", after, before.BoundAt)
	}
	if _, ok := srv.room("alpha", strconv.FormatInt(groupChatID, 10)); ok {
		t.Fatalf("the old chat is still a room: %+v", srv.rooms("alpha"))
	}

	const inbound = "@alpha from the supergroup"
	tg.post(upgraded, "supergroup", inbound, operator)
	srv.waitForMessage(inbound)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": "@beta after the upgrade"}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	tg.waitSentFrom(t, upgraded, "alpha", "after the upgrade")
}
