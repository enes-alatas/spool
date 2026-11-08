//go:build integration

package itest

import (
	"strconv"
	"testing"
	"time"
)

// waitRoom polls until the loop has a row for roomID, and returns it.
func (srv *server) waitRoom(loop, roomID string) roomJSON {
	srv.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if room, ok := srv.room(loop, roomID); ok {
			return room
		}
	}
	srv.t.Fatalf("%s never recorded room %s: %+v", loop, roomID, srv.rooms(loop))
	return roomJSON{}
}

// A loop the operator took out of the fleet channel has no fleet channel
// to bind a first group to, so the group it hears is recorded unbound, for
// the operator to bind from the loop page, and the fleet channel gains no
// room (#1424, ADR-0038).
func TestALoopOutsideTheFleetChannelBindsNoFirstGroup(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8181, First: "Operator", Username: "operator"}
	outside := map[string]any{"in_fleet_channel": false}
	srv, _ := startTelegramFleet(t, operator, outside, outside)
	groupID := strconv.FormatInt(groupChatID, 10)
	for _, name := range []string{"alpha", "beta"} {
		if room := srv.waitRoom(name, groupID); room.Channel != "" {
			t.Fatalf("%s outside the fleet channel bound its first group as %+v, want it unbound", name, room)
		}
		if loop := srv.loop(name); loop.TGGroupChatID != 0 {
			t.Fatalf("%s's fleet room = %d, want none", name, loop.TGGroupChatID)
		}
	}
	if loops := srv.channel("group").Loops; len(loops) != 0 {
		t.Fatalf("the fleet channel's loops = %v, want none", loops)
	}
	// the operator binds it where it belongs, from the loop page
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "pair"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/pair/loops/"+name, nil, 204, "")
		srv.wantRefusal("PUT", "/api/loops/"+name+"/rooms", roomBody(groupChatID, "pair"), 200, "")
	}
}

// The same on Slack: terra, outside the fleet channel, records its first
// channel unbound, while milo, in it with no fleet room, binds its first
// to the fleet channel as before (#1424).
func TestASlackLoopOutsideTheFleetChannelBindsNoFirstRoom(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleetWith(t, map[string]any{"in_fleet_channel": false})
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000030")
	slack.pushMessage(t, slackMiloAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000030")
	srv.waitSlackLink("milo", func(link slackStatus) bool { return link.ChannelID == slackChannel })
	if room := srv.waitRoom("terra", slackChannel); room.Channel != "" {
		t.Fatalf("terra outside the fleet channel bound its first room as %+v, want it unbound", room)
	}
	if room, _ := srv.room("milo", slackChannel); room.Channel != "group" {
		t.Fatalf("milo's first room = %+v, want it bound to the fleet channel", room)
	}
	var status slackStatus
	srv.mustJSON("GET", "/api/loops/terra/slack/status", nil, &status)
	if status.ChannelID != "" {
		t.Fatalf("terra's fleet room = %q, want none", status.ChannelID)
	}
}
