//go:build integration

package itest

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// A second Slack channel the apps hear from is recorded unbound, under the
// name Slack gives it, and ingests nothing, fleet's room
// staying where it was. Once the operator binds it to a channel, the
// channel's posts go there and not to the fleet room, a person's message
// there arrives as the channel's, stored once, and a reply by ref threads
// under it in that room (ADR-0038, #548).
func TestASlackRoomCarriesItsChannel(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	apps := []string{slackAppToken, slackMiloAppToken}
	for _, app := range apps {
		slack.pushMessage(t, app, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	}
	for _, name := range []string{"terra", "milo"} {
		srv.waitRoom(name, slackChannel)
	}
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"terra", "milo"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	const backendRoom = "C0BACKEND"
	for _, app := range apps {
		slack.pushMessage(t, app, "channel", backendRoom, slackOperator, "<@U0TERRA> before the room is bound", "1727600000.000020")
	}
	deadline := time.Now().Add(20 * time.Second)
	for _, name := range []string{"terra", "milo"} {
		for {
			if room, ok := srv.room(name, backendRoom); ok && room.Title != "" {
				if room.Surface != "slack" || room.Channel != "" || room.Title != "c0backend" {
					t.Fatalf("%s heard the new channel as %+v, want it unbound and named", name, room)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never recorded the new channel: %+v", name, srv.rooms(name))
			}
			time.Sleep(200 * time.Millisecond)
		}
		if fleet, ok := srv.room(name, slackChannel); !ok || fleet.Channel != fleetChannel {
			t.Fatalf("a second channel moved %s's fleet room: %+v", name, srv.rooms(name))
		}
	}
	if got := srv.activityWith("@terra before the room is bound"); len(got) != 0 {
		t.Fatalf("a message in an unbound room was ingested: %s", dump(got))
	}

	for _, name := range []string{"terra", "milo"} {
		srv.mustJSON("PUT", "/api/loops/"+name+"/rooms",
			map[string]any{"surface": "slack", "room_id": backendRoom, "channel": "backend"}, nil)
	}

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	for _, send := range []map[string]any{
		{"destination": "channel:backend", "text": "@milo backend words"},
		{"destination": "channel:fleet", "text": "@milo fleet words"},
	} {
		if res := callSend(t, terra, send); res.IsError {
			t.Fatalf("send %v refused: %s", send, resultText(res))
		}
	}
	if post := slack.waitPost(t, "backend words"); post.Channel != backendRoom || post.Token != slackBotToken {
		t.Fatalf("the channel's post = %+v, want terra's app in %s", post, backendRoom)
	}
	if post := slack.waitPost(t, "fleet words"); post.Channel != slackChannel {
		t.Fatalf("fleet's post = %+v, want it in %s", post, slackChannel)
	}
	for _, taken := range slack.postsTaken() {
		if strings.Contains(taken.Text, "backend words") && taken.Channel != backendRoom {
			t.Fatalf("the channel's post reached another room: %+v", taken)
		}
	}

	const inboundTS = "1727600000.000030"
	for _, app := range apps {
		slack.pushMessage(t, app, "channel", backendRoom, slackOperator, "<@U0TERRA> from the backend room", inboundTS)
	}
	const inbound = "@terra from the backend room"
	srv.waitForMessage(inbound)
	srv.waitInput("terra", "via slack · channel:backend · ref:", inbound)
	time.Sleep(time.Second) // let a second ingester add its row, if there were one
	stored := srv.activityWith(inbound)
	if len(stored) != 1 || stored[0].Channel != "backend" || stored[0].Conversation != "group" {
		t.Fatalf("the room's message was stored as %s, want once, in backend", dump(stored))
	}

	reply := map[string]any{"destination": "channel:backend", "text": "@milo threaded answer",
		"reply_to": "ref:" + strconv.FormatInt(stored[0].ID, 10)}
	if res := callSend(t, terra, reply); res.IsError {
		t.Fatalf("reply refused: %s", resultText(res))
	}
	if post := slack.waitPost(t, "threaded answer"); post.Channel != backendRoom || post.ThreadTS != inboundTS {
		t.Errorf("the reply = %+v, want it threaded under %s in %s", post, inboundTS, backendRoom)
	}

	// A room bound by its pasted id, before the app heard from it, is
	// named at its first message, as an unbound one is.
	const docsRoom = "C0DOCS"
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "docs"}, nil)
	srv.wantRefusal("PUT", "/api/channels/docs/loops/terra", nil, 204, "")
	var pasted roomJSON
	srv.mustJSON("PUT", "/api/loops/terra/rooms",
		map[string]any{"surface": "slack", "room_id": docsRoom, "channel": "docs"}, &pasted)
	if pasted.Channel != "docs" || pasted.Title != "" {
		t.Fatalf("bound by id = %+v, want an untitled room bound to docs", pasted)
	}
	slack.pushMessage(t, slackAppToken, "channel", docsRoom, slackOperator, "<@U0TERRA> in the docs room", "1727600000.000040")
	srv.waitInput("terra", "via slack · channel:docs · ref:", "@terra in the docs room")
	if room, _ := srv.room("terra", docsRoom); room.Title != "c0docs" || room.Channel != "docs" {
		t.Errorf("the pasted room after its first message = %+v, want it named and still bound", room)
	}
}

// pushLeft sends app a member_left_channel event: user left channel.
func (slack *fakeSlack) pushLeft(t *testing.T, app, channel, user string) {
	t.Helper()
	slack.pushEvent(t, app, map[string]any{"type": "member_left_channel", "user": user,
		"channel": channel, "channel_type": "C", "team": "T0ACME", "ts": "left-" + user})
}

// pushSelfLeft sends app the event Slack sends the leaver itself, kind
// channel_left or group_left: it names no user, since the app is the one
// that left.
func (slack *fakeSlack) pushSelfLeft(t *testing.T, app, kind, channel string) {
	t.Helper()
	slack.pushEvent(t, app, map[string]any{"type": kind, "channel": channel,
		"actor_id": slackOperator, "event_ts": "1727600000.000040", "ts": kind})
}

// A loop's Slack app removed from a Slack channel forgets its room there,
// whichever event Slack tells it by: channel_left or group_left, sent to
// the app that left, or member_left_channel naming its bot user. Another
// member leaving changes no room (ADR-0070).
func TestASlackAppRemovedFromAChannelForgetsItsRoom(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)

	// one app's events are taken in order: once the marker channel is
	// recorded, the person's leaving before it has been seen
	slack.pushLeft(t, slackAppToken, slackChannel, slackOperator)
	const marker = "C0MARKER"
	slack.pushMessage(t, slackAppToken, "channel", marker, slackOperator, "marker", "1727600000.000030")
	srv.waitRoom("terra", marker)
	if room, ok := srv.room("terra", slackChannel); !ok || room.Channel != fleetChannel {
		t.Fatalf("a person leaving changed terra's room: %+v", srv.rooms("terra"))
	}

	slack.pushLeft(t, slackAppToken, slackChannel, terraBot.UserID)
	srv.waitNoRoom("terra", slackChannel)
	if room, ok := srv.room("milo", slackChannel); !ok || room.Channel != fleetChannel {
		t.Errorf("terra's app leaving changed milo's room: %+v", srv.rooms("milo"))
	}

	slack.pushSelfLeft(t, slackMiloAppToken, "channel_left", slackChannel)
	srv.waitNoRoom("milo", slackChannel)
	slack.pushSelfLeft(t, slackAppToken, "group_left", marker)
	srv.waitNoRoom("terra", marker)
}

// pushJoined sends app a member_joined_channel event: user joined channel,
// added by inviter, or by no one Slack names when inviter is "".
func (slack *fakeSlack) pushJoined(t *testing.T, app, channel, user, inviter string) {
	t.Helper()
	event := map[string]any{"type": "member_joined_channel", "user": user, "channel": channel,
		"channel_type": "C", "team": "T0ACME", "ts": "joined-" + user + "-" + inviter}
	if inviter != "" {
		event["inviter"] = inviter
	}
	slack.pushEvent(t, app, event)
}

// waitTitledRoom polls until the loop's room for roomID is recorded with a
// title, which the app asks Slack for once the room is recorded.
func (srv *server) waitTitledRoom(loop, roomID string) roomJSON {
	srv.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if room, ok := srv.room(loop, roomID); ok && room.Title != "" {
			return room
		}
	}
	srv.t.Fatalf("%s never recorded room %s with a title: %+v", loop, roomID, srv.rooms(loop))
	return roomJSON{}
}

// An allowed person inviting a loop's Slack app to a Slack channel records
// the room, titled, before anyone speaks there: unbound, or bound through
// the chat of a channel the loop is in. An invite by a stranger, one that
// names no inviter, and another member joining record nothing (ADR-0070).
func TestAnAllowedPersonInvitingTheAppRecordsTheChannel(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const strangers, selfJoined, othersJoined, invited = "C0STRANGE", "C0SELF", "C0OTHER", "C0INVITED"

	// one app's events are taken in order: once the allowed invite is
	// recorded, the three before it have been seen and turned away
	slack.pushJoined(t, slackAppToken, strangers, terraBot.UserID, "U0STRANGER")
	slack.pushJoined(t, slackAppToken, selfJoined, terraBot.UserID, "")
	slack.pushJoined(t, slackAppToken, othersJoined, miloBot.UserID, slackOperator)
	slack.pushJoined(t, slackAppToken, invited, terraBot.UserID, slackOperator)
	if room := srv.waitTitledRoom("terra", invited); room.Channel != "" || room.Title != "c0invited" {
		t.Fatalf("the invited channel = %+v, want it recorded unbound and titled", room)
	}
	for _, roomID := range []string{strangers, selfJoined, othersJoined} {
		if room, ok := srv.room("terra", roomID); ok {
			t.Errorf("an invite that should record nothing recorded %+v", room)
		}
	}
	if got := srv.activityWith("c0invited"); len(got) != 0 {
		t.Errorf("an invite was ingested as a message: %s", dump(got))
	}

	// a Slack channel that is the chat of a channel terra is in binds as
	// it arrives
	const backendRoom = "C0BACKEND"
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	srv.wantRefusal("PUT", "/api/channels/backend/loops/terra", nil, 204, "")
	srv.mustJSON("PUT", "/api/channels/backend/chat", map[string]any{"surface": "slack", "chat_id": backendRoom}, nil)
	slack.pushJoined(t, slackAppToken, backendRoom, terraBot.UserID, slackOperator)
	srv.waitRoomChannel("terra", backendRoom, "backend")
}
