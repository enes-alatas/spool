//go:build integration

package itest

import (
	"strconv"
	"testing"
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
