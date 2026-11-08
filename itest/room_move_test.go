//go:build integration

package itest

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// A group every loop in it carries for one channel moves to another
// channel they are all in in one step, from either loop's page, and the
// old channel no longer carries it. A loop that carries it and isn't in
// the new channel stops the move, named in the refusal (#1424).
func TestAGroupTheLoopsShareMovesTogether(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8282, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)
	groupID := strconv.FormatInt(groupChatID, 10)
	for _, name := range []string{"alpha", "beta"} {
		if room, _ := srv.room(name, groupID); room.Channel != "group" {
			t.Fatalf("%s's first group = %+v, want it bound to the fleet channel", name, room)
		}
	}
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "solo"}, nil)
	srv.wantRefusal("PUT", "/api/channels/solo/loops/alpha", nil, 204, "")
	resp, out := srv.do("PUT", "/api/loops/alpha/rooms", roomBody(groupChatID, "solo"))
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "not_in_channel" || !strings.Contains(string(out), "beta") {
		t.Fatalf("moving a group beta carries to a channel beta isn't in = %d %s, want 409 not_in_channel naming beta", resp.StatusCode, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if room, _ := srv.room(name, groupID); room.Channel != "group" {
			t.Fatalf("a refused move left %s's group as %+v, want it on the fleet channel", name, room)
		}
	}

	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}
	var moved roomJSON
	srv.mustJSON("PUT", "/api/loops/beta/rooms", roomBody(groupChatID, "backend"), &moved)
	if moved.Channel != "backend" {
		t.Fatalf("the move answered %+v, want the room on backend", moved)
	}
	for _, name := range []string{"alpha", "beta"} {
		if room, _ := srv.room(name, groupID); room.Channel != "backend" || room.BoundAt == 0 {
			t.Fatalf("%s's group after the move = %+v, want it bound to backend", name, room)
		}
		if loop := srv.loop(name); loop.TGGroupChatID != 0 {
			t.Fatalf("the fleet channel still carries the group for %s", name)
		}
	}
}
