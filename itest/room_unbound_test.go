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

// No group binds itself (ADR-0068): the first group a loop's bot hears an
// allowed sender in is recorded unbound, for the operator to bind from the
// loop page, even for a loop in a channel, and every later group the same.
func TestNoTelegramGroupBindsItself(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8181, First: "Operator", Username: "operator"}
	outside := map[string]any{"fleet": false}
	srv, tg := startTelegramFleet(t, operator, outside, outside)
	for _, name := range []string{"alpha", "beta"} {
		srv.joinChannel(fleetChannel, name)
	}
	const laterGroup int64 = -1005550001
	tg.post(laterGroup, "supergroup", "morning", operator)
	for _, chatID := range []int64{groupChatID, laterGroup} {
		for _, name := range []string{"alpha", "beta"} {
			if room := srv.waitRoom(name, strconv.FormatInt(chatID, 10)); room.Channel != "" {
				t.Fatalf("%s bound group %d by itself as %+v, want it unbound", name, chatID, room)
			}
		}
	}
	// the operator binds it where it belongs, from the loop page
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/loops/"+name+"/rooms", roomBody(groupChatID, fleetChannel), 200, "")
	}
}

// The same on Slack: milo, in a channel with a bound room, and terra, in
// none, both record a channel they hear first unbound (ADR-0068).
func TestNoSlackChannelBindsItself(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleetWith(t, map[string]any{"fleet": false})
	const laterChannel = "C0LATER"
	slack.pushMessage(t, slackAppToken, "channel", laterChannel, slackOperator, "morning", "1727600000.000030")
	slack.pushMessage(t, slackMiloAppToken, "channel", laterChannel, slackOperator, "morning", "1727600000.000030")
	for _, name := range []string{"terra", "milo"} {
		if room := srv.waitRoom(name, laterChannel); room.Channel != "" {
			t.Fatalf("%s bound %s by itself as %+v, want it unbound", name, laterChannel, room)
		}
	}
}
