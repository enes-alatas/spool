//go:build integration

package itest

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// A second Slack channel the apps hear from is recorded unbound, under the
// name Slack gives it, and ingests nothing, the fleet channel's room
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
		srv.waitSlackLink(name, func(link slackStatus) bool { return link.ChannelID == slackChannel })
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
		if fleet, ok := srv.room(name, slackChannel); !ok || fleet.Channel != "group" {
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
		{"destination": "group", "text": "@milo fleet words"},
	} {
		if res := callSend(t, terra, send); res.IsError {
			t.Fatalf("send %v refused: %s", send, resultText(res))
		}
	}
	if post := slack.waitPost(t, "backend words"); post.Channel != backendRoom || post.Token != slackBotToken {
		t.Fatalf("the channel's post = %+v, want terra's app in %s", post, backendRoom)
	}
	if post := slack.waitPost(t, "fleet words"); post.Channel != slackChannel {
		t.Fatalf("the fleet channel's post = %+v, want it in %s", post, slackChannel)
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
