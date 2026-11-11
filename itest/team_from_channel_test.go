//go:build integration

package itest

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A team made from a hub channel takes it as its home, with its history,
// its members and its bound rooms, and every loop in it joins the team.
// It is an explicit ask: a create whose name is only a channel's is
// refused as before. A channel that is a team's, or holds a loop in one,
// is refused, and a refused create changes nothing (ADR-0068).
func TestATeamIsMadeFromAChannel(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6262, First: "Operator", Username: "operator"}
	hub, _ := startTelegramFleet(t, operator) // alpha and beta, each with a bot

	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "evalskills"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		hub.wantRefusal("PUT", "/api/channels/evalskills/loops/"+name, nil, 204, "")
	}
	const evalRoom int64 = -1009000000088
	hub.mustJSON("PUT", "/api/loops/alpha/rooms", roomBody(evalRoom, "evalskills"), nil)
	hub.mustJSON("POST", "/api/channels/evalskills/messages", map[string]any{"text": "said before the team"}, nil)

	// a name that is only a channel's moves no loop
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "evalskills"}, 409, "channel_exists")
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "evals", "home_channel": "evals", "from_channel": "evalskills"}, 400, "")
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "evals", "from_channel": "nowhere"}, 404, "channel_not_found")
	wantUntouched := func(when string) {
		t.Helper()
		hub.wantRefusal("GET", "/api/teams/evals", nil, 404, "team_not_found")
		hub.wantRefusal("GET", "/api/teams/evalskills", nil, 404, "team_not_found")
		for _, name := range []string{"alpha", "beta"} {
			if team := hub.loop(name).Team; team != "" {
				t.Errorf("%s: %s is in team %q, want none", when, name, team)
			}
		}
		if channel := hub.channel("evalskills"); channel.Team != "" {
			t.Errorf("%s: evalskills = %+v, want it a hub channel", when, channel)
		}
	}
	wantUntouched("after the refusals")

	var evals teamJSON
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "evals", "description": "Grades the loops.", "from_channel": "evalskills"}, &evals)
	if evals.HomeChannel != "evalskills" || !reflect.DeepEqual(evals.Channels, []string{"evalskills"}) ||
		!reflect.DeepEqual(evals.Loops, []string{"alpha", "beta"}) {
		t.Fatalf("evals = %+v, want evalskills its home and only channel, alpha and beta in it", evals)
	}
	if channel := hub.channel("evalskills"); channel.Team != "evals" || !reflect.DeepEqual(channel.Loops, []string{"alpha", "beta"}) {
		t.Errorf("evalskills = %+v, want evals's, with both loops still in it", channel)
	}
	for _, name := range []string{"alpha", "beta"} {
		if team := hub.loop(name).Team; team != "evals" {
			t.Errorf("%s is in team %q, want evals", name, team)
		}
	}
	if room, _ := hub.room("alpha", strconv.FormatInt(evalRoom, 10)); room.Channel != "evalskills" {
		t.Errorf("alpha's evalskills room = %+v, want it still bound", room)
	}
	var said []struct {
		Text string `json:"text"`
	}
	hub.mustJSON("GET", "/api/channels/evalskills/messages", nil, &said)
	kept := false
	for _, message := range said {
		kept = kept || message.Text == "said before the team"
	}
	if !kept {
		t.Errorf("evalskills's history lost what was said before the team: %+v", said)
	}

	// a channel that is a team's, or holds a loop in one, is refused
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "evals", "from_channel": fleetChannel}, 409, "team_exists")
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "graders", "from_channel": "evalskills"}, 409, "channel_in_other_team")
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "mixed"}, nil)
	hub.wantRefusal("PUT", "/api/channels/mixed/loops/alpha", nil, 204, "")
	resp, out := hub.do("POST", "/api/teams", map[string]any{"name": "graders", "from_channel": "mixed"})
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "channel_loops_in_other_team" || !strings.Contains(string(out), "alpha (evals)") {
		t.Errorf("graders from mixed, alpha of evals in it = %d %s, want 409 channel_loops_in_other_team naming alpha and evals", resp.StatusCode, out)
	}
	hub.wantRefusal("GET", "/api/teams/graders", nil, 404, "team_not_found")
	if channel := hub.channel("mixed"); channel.Team != "" {
		t.Errorf("mixed after the refusal = %+v, want it a hub channel", channel)
	}
}

// A loop that joins a team made from its channel reads its team in the
// prompt at its next wake, as after any move into a team (ADR-0066).
func TestALoopJoiningATeamFromItsChannelIsToldItsTeam(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.createLoop("aster", map[string]any{"workspace_path": workspaceWithScript(t, "!echo\n"), "workspace_mode": "dir", "fleet": false})
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "evalskills"}, nil)
	hub.wantRefusal("PUT", "/api/channels/evalskills/loops/aster", nil, 204, "")
	// aster's session starts before the team, so its wake below carries
	// the change as a standing-instructions note
	hub.message("aster", "first")
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	hub.waitState("aster", "asleep", 30*time.Second)

	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "evals", "description": "Grades the loops.", "from_channel": "evalskills"}, nil)
	at := time.Now().UnixMilli()
	hub.message("aster", "who is there")
	note := hub.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "who is there")
	}).ResultText
	if !strings.Contains(note, "- You are in the team evals — Grades the loops.") || !strings.Contains(note, "    channel:evalskills") {
		t.Errorf("aster's wake after evals was made from its channel does not name the team and its home:\n%s", note)
	}
}
