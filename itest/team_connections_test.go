//go:build integration

package itest

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// teamConnectionJSON is what this test reads of a connection view.
type teamConnectionJSON struct {
	Name  string   `json:"name"`
	Loops []string `json:"loops"`
	Teams []string `json:"teams"`
}

// A connection attached to a team reaches every loop in it, from its next
// turn, and leaves a loop that leaves the team. A team attach, or a loop
// joining a team, that a loop couldn't hold beside its own connections is
// refused, naming the loop or the connection, and so is one clashing with
// a connection the team already holds, though it has no loops yet. A
// connection a team holds isn't deleted, and a deleted team takes its
// attachments (#1610).
func TestAConnectionAttachedToATeamReachesItsLoops(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red"}, nil)
	// turn 1 is the creation tick's
	workspace := workspaceWithScript(t, "!echo\n!env MARKER\n!env MARKER\n")
	hub.createLoop("aster", map[string]any{"team": "red", "workspace_path": workspace})
	hub.createLoop("briar", map[string]any{"team": "red"})
	hub.createLoop("cedar", nil)
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	envVar := func(name, env, owner string) {
		body := map[string]any{"name": name, "kind": "env-var", "config": map[string]any{"env": env}, "secret": name + "1"}
		if owner != "" {
			body["owner_loop"] = owner
		}
		hub.mustJSON("POST", "/api/connections", body, nil)
	}
	envVar("reds", "MARKER", "")
	envVar("cedars", "MARKER", "cedar")
	envVar("briars", "OTHER", "briar")
	envVar("others", "OTHER", "")

	hub.wantRefusal("PUT", "/api/teams/red/connections/cedars", nil, 409, "connection_private")
	hub.wantRefusal("PUT", "/api/teams/green/connections/reds", nil, 404, "team_not_found")
	hub.wantRefusal("PUT", "/api/teams/red/connections/nope", nil, 404, "connection_not_found")
	// briar sets OTHER itself, so the team can't give it another OTHER
	resp, body := hub.do("PUT", "/api/teams/red/connections/others", nil)
	if resp.StatusCode != 409 || errorCode(body) != "connection_env_taken" || !strings.Contains(string(body), "briar") {
		t.Fatalf("attaching others to red = %d %s, want 409 connection_env_taken naming briar", resp.StatusCode, body)
	}
	// a team with no loops yet is checked against its own connections:
	// otherwise it would take both MARKERs and refuse every loop after
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "blue"}, nil)
	envVar("markers", "MARKER", "")
	hub.mustJSON("PUT", "/api/teams/blue/connections/reds", nil, nil)
	resp, body = hub.do("PUT", "/api/teams/blue/connections/markers", nil)
	if resp.StatusCode != 409 || errorCode(body) != "connection_env_taken" || !strings.Contains(string(body), "reds") {
		t.Fatalf("attaching markers to blue = %d %s, want 409 connection_env_taken naming reds", resp.StatusCode, body)
	}
	hub.mustJSON("DELETE", "/api/teams/blue/connections/reds", nil, nil)

	hub.mustJSON("PUT", "/api/teams/red/connections/reds", nil, nil)
	var reds teamConnectionJSON
	hub.mustJSON("GET", "/api/connections/reds", nil, &reds)
	if !reflect.DeepEqual(reds.Teams, []string{"red"}) || len(reds.Loops) != 0 {
		t.Fatalf("reds = %+v, want attached to red alone", reds)
	}
	want := []loopConnectionJSON{{Name: "briars", Kind: "env-var"}, {Name: "reds", Kind: "env-var", Team: "red"}}
	if got := hub.loopConnections("briar"); !reflect.DeepEqual(got, want) {
		t.Fatalf("briar holds %+v, want its own briars and red's reds", got)
	}
	hub.message("aster", "what do you hold?")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=reds1" })

	// cedar sets MARKER itself, so it can't join a team that gives one
	resp, body = hub.do("PATCH", "/api/loops/cedar", map[string]any{"team": "red"})
	if resp.StatusCode != 409 || errorCode(body) != "connection_env_taken" || !strings.Contains(string(body), "reds") {
		t.Fatalf("moving cedar into red = %d %s, want 409 connection_env_taken naming reds", resp.StatusCode, body)
	}
	if team := hub.loop("cedar").Team; team != "" {
		t.Fatalf("cedar reads team %q after the refused move, want none", team)
	}

	// aster leaves red, and its next turn runs without the team's env
	hub.waitState("aster", "asleep", 60*time.Second)
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": ""}, nil)
	hub.message("aster", "and now?")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=" })

	hub.wantRefusal("DELETE", "/api/connections/reds", nil, 409, "connection_attached")
	hub.mustJSON("PATCH", "/api/loops/briar", map[string]any{"team": ""}, nil)
	hub.mustJSON("DELETE", "/api/teams/red", nil, nil)
	hub.mustJSON("GET", "/api/connections/reds", nil, &reds)
	if len(reds.Teams) != 0 {
		t.Fatalf("reds after red's delete = %+v, want attached to no team", reds)
	}
	hub.mustJSON("DELETE", "/api/connections/reds", nil, nil)
}
