//go:build integration

package itest

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// A team made through the API has a home channel named after it, which
// every loop put in the team joins; a team's channels take only its loops,
// a channel goes to a team only with no outsider in it, and a loop leaving
// its team leaves every channel of it, its rooms there unbound (ADR-0066).
func TestATeamsChannelsAreKeptToItsLoops(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6161, First: "Operator", Username: "operator"}
	hub, _ := startTelegramFleet(t, operator) // alpha and beta, each with a bot

	var red teamJSON
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red"}, &red)
	if red.HomeChannel != "red" || !reflect.DeepEqual(red.Channels, []string{"red"}) {
		t.Fatalf("red = %+v, want a home channel named red", red)
	}
	if channel := hub.channel("red"); channel.Team != "red" || len(channel.Loops) != 0 {
		t.Fatalf("red's home channel = %+v, want red's, empty", channel)
	}
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "crew", "home_channel": "red"}, 409, "channel_exists")
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "red_ops"}, 400, "channel_name_invalid")
	var named teamJSON
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red_ops", "home_channel": "red-ops"}, &named)
	if named.HomeChannel != "red-ops" {
		t.Errorf("red_ops = %+v, want its home channel named red-ops", named)
	}

	hub.mustJSON("PATCH", "/api/loops/alpha", map[string]any{"team": "red"}, nil)
	if got := hub.channel("red").Loops; !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("red's home channel after alpha joined = %v, want alpha", got)
	}
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "red-side", "team": "red"}, nil)
	hub.wantRefusal("POST", "/api/channels", map[string]any{"name": "nowhere", "team": "green"}, 404, "team_not_found")
	resp, out := hub.do("PUT", "/api/channels/red-side/loops/beta", nil)
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "channel_in_other_team" || !strings.Contains(string(out), "beta") {
		t.Errorf("beta, in no team, put in red-side = %d %s, want 409 channel_in_other_team naming beta", resp.StatusCode, out)
	}
	hub.wantRefusal("PUT", "/api/channels/red-side/loops/alpha", nil, 204, "")

	// an existing channel goes to a team only whole
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "ops"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		hub.wantRefusal("PUT", "/api/channels/ops/loops/"+name, nil, 204, "")
	}
	resp, out = hub.do("PATCH", "/api/channels/ops", map[string]any{"team": "red"})
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "channel_has_outsiders" || !strings.Contains(string(out), "beta") || strings.Contains(string(out), "alpha") {
		t.Errorf("giving red ops with beta in it = %d %s, want 409 channel_has_outsiders naming beta alone", resp.StatusCode, out)
	}
	hub.wantRefusal("PATCH", "/api/channels/group", map[string]any{"team": "red"}, 400, "channel_reserved")
	hub.wantRefusal("PATCH", "/api/channels/red", map[string]any{"team": ""}, 409, "channel_is_home")
	hub.wantRefusal("DELETE", "/api/channels/ops/loops/beta", nil, 204, "")
	var given channelJSON
	hub.mustJSON("PATCH", "/api/channels/ops", map[string]any{"team": "red"}, &given)
	if given.Team != "red" {
		t.Fatalf("ops given to red = %+v", given)
	}
	hub.wantRefusal("PATCH", "/api/teams/red_ops", map[string]any{"home_channel": "ops"}, 409, "channel_not_in_team")
	hub.wantRefusal("PATCH", "/api/teams/red", map[string]any{"home_channel": "nowhere"}, 404, "channel_not_found")

	// a loop in red can't send to another team's channel: it isn't in it
	hub.mustJSON("PATCH", "/api/loops/beta", map[string]any{"team": "red_ops"}, nil)
	alpha := mcpSession(t, hub, hubMCPToken(t, hub, "alpha"))
	wantSendError(t, callSend(t, alpha, map[string]any{"destination": "channel:red-ops", "text": "@beta hello"}), "no_such_destination")

	// leaving the team leaves its channels and unbinds its rooms there
	const sideRoom int64 = -1009000000077
	hub.mustJSON("PUT", "/api/loops/alpha/rooms", roomBody(sideRoom, "red-side"), nil)
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "hubwide"}, nil)
	hub.wantRefusal("PUT", "/api/channels/hubwide/loops/alpha", nil, 204, "")
	hub.mustJSON("PATCH", "/api/loops/alpha", map[string]any{"team": ""}, nil)
	for channel, want := range map[string][]string{"red": {}, "red-side": {}, "ops": {}, "hubwide": {"alpha"}, "group": {"alpha", "beta"}} {
		if got := hub.channel(channel).Loops; !reflect.DeepEqual(got, want) {
			t.Errorf("%s after alpha left red = %v, want %v", channel, got, want)
		}
	}
	if room, _ := hub.room("alpha", strconv.FormatInt(sideRoom, 10)); room.Channel != "" {
		t.Errorf("alpha left red and its red-side room = %+v, want it unbound", room)
	}

	// deleting the team deletes its channels with it
	hub.wantRefusal("DELETE", "/api/teams/red", nil, 204, "")
	for _, channel := range []string{"red", "red-side", "ops"} {
		hub.wantRefusal("GET", "/api/channels/"+channel, nil, 404, "channel_not_found")
	}
}

// A team without a home channel, as the upgrade's default is, gets one
// by naming a channel of its own, and every loop of the team is put in it.
func TestATeamNamesItsHomeChannel(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		hub.createLoop(name, nil)
	}
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "crew"}, nil)
	for _, name := range []string{"aster", "briar"} {
		hub.mustJSON("PATCH", "/api/loops/"+name, map[string]any{"team": "crew"}, nil)
	}
	var crew teamJSON
	hub.mustJSON("PATCH", "/api/teams/crew", map[string]any{"home_channel": ""}, &crew)
	if crew.HomeChannel != "" || !reflect.DeepEqual(crew.Channels, []string{"crew"}) {
		t.Fatalf("crew with no home = %+v, want crew still its channel", crew)
	}
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "deck", "team": "crew"}, nil)
	hub.mustJSON("PATCH", "/api/teams/crew", map[string]any{"home_channel": "deck"}, &crew)
	if crew.HomeChannel != "deck" || !reflect.DeepEqual(hub.channel("deck").Loops, []string{"aster", "briar"}) {
		t.Fatalf("crew with deck as home = %+v, deck holds %v, want both loops in it", crew, hub.channel("deck").Loops)
	}
	hub.createLoop("cedar", map[string]any{"team": "crew"})
	if got := hub.channel("deck").Loops; !reflect.DeepEqual(got, []string{"aster", "briar", "cedar"}) {
		t.Errorf("deck after cedar was made in crew = %v, want all three", got)
	}
}
