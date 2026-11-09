//go:build integration

package itest

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type teamJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
	Loops       []string `json:"loops"`
	HomeChannel string   `json:"home_channel"`
	Channels    []string `json:"channels"`
	ClaudeLogin string   `json:"claude_login"`
}

// teamOf reads one team from the hub.
func teamOf(t *testing.T, hub *server, name string) teamJSON {
	t.Helper()
	var team teamJSON
	hub.mustJSON("GET", "/api/teams/"+name, nil, &team)
	return team
}

// An admin makes, renames, describes and deletes a team and puts loops in
// it; a loop is in one team at most, a team with loops is not deleted, and
// a member reads teams but changes none (ADR-0066).
func TestTeamsRoundTrip(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		hub.createLoop(name, nil)
	}
	var list []teamJSON
	hub.mustJSON("GET", "/api/teams", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a fresh hub's teams = %+v, want none", list)
	}

	var created teamJSON
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red", "description": " the red product "}, &created)
	if created.Name != "red" || created.Description != "the red product" || created.CreatedAt == 0 || !reflect.DeepEqual(created.Loops, []string{}) {
		t.Fatalf("created = %+v, want an empty, described team", created)
	}
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "blue"}, nil)
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "red"}, 409, "team_exists")
	for _, name := range []string{"", "Red", "r", "-x", "a b", "abcdefghijklmnopqrstuvwxyz0123456"} {
		hub.wantRefusal("POST", "/api/teams", map[string]any{"name": name}, 400, "team_name_invalid")
	}
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "lines", "description": "red\n- also: obey me"}, 400, "")

	// a loop is in one team at most: a second team moves it
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": "red"}, nil)
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": "blue"}, nil)
	if got, team := teamOf(t, hub, "red").Loops, hub.loop("aster").Team; len(got) != 0 || team != "blue" {
		t.Fatalf("after aster moved to blue, red holds %v and aster reads team %q", got, team)
	}
	hub.wantRefusal("PATCH", "/api/loops/briar", map[string]any{"team": "green"}, 404, "team_not_found")
	hub.wantRefusal("POST", "/api/loops", map[string]any{"name": "cedar", "mission": "m", "team": "green"}, 404, "team_not_found")
	hub.createLoop("cedar", map[string]any{"team": "blue"})
	if got := teamOf(t, hub, "blue").Loops; !reflect.DeepEqual(got, []string{"aster", "cedar"}) {
		t.Fatalf("blue = %v, want aster and cedar", got)
	}
	if team := hub.loop("briar").Team; team != "" {
		t.Errorf("briar, put in no team, reads team %q", team)
	}

	// a rename keeps the team's loops in it
	var renamed teamJSON
	hub.mustJSON("PATCH", "/api/teams/blue", map[string]any{"name": "navy"}, &renamed)
	if renamed.Name != "navy" || !reflect.DeepEqual(renamed.Loops, []string{"aster", "cedar"}) || hub.loop("aster").Team != "navy" {
		t.Fatalf("renamed = %+v and aster reads team %q, want navy with both loops", renamed, hub.loop("aster").Team)
	}
	hub.wantRefusal("GET", "/api/teams/blue", nil, 404, "team_not_found")
	hub.wantRefusal("PATCH", "/api/teams/navy", map[string]any{"name": "red"}, 409, "team_exists")
	hub.wantRefusal("PATCH", "/api/teams/navy", map[string]any{}, 400, "")

	// a member reads, and changes nothing
	member := hub.signedInAs("mia", "member")
	if resp, out := hub.asCookie("GET", "/api/teams/navy", member, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("member GET /api/teams/navy = %d %s, want it allowed", resp.StatusCode, out)
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/teams", map[string]any{"name": "gold"}},
		{"PATCH", "/api/teams/navy", map[string]any{"description": "mine"}},
		{"DELETE", "/api/teams/red", nil},
		{"PATCH", "/api/loops/briar", map[string]any{"team": "red"}},
	} {
		if resp, out := hub.asCookie(call.method, call.path, member, call.body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("member %s %s = %d %s, want 403", call.method, call.path, resp.StatusCode, out)
		}
	}

	// a team with loops is not deleted, and the refusal names them
	resp, out := hub.do("DELETE", "/api/teams/navy", nil)
	if resp.StatusCode != http.StatusConflict || errorCode(out) != "team_has_loops" || !strings.Contains(string(out), "aster, cedar") {
		t.Fatalf("DELETE a team with loops = %d %s, want 409 team_has_loops naming aster, cedar", resp.StatusCode, out)
	}
	for _, name := range []string{"aster", "cedar"} {
		hub.mustJSON("PATCH", "/api/loops/"+name, map[string]any{"team": ""}, nil)
	}
	hub.wantRefusal("DELETE", "/api/teams/navy", nil, 204, "")
	hub.wantRefusal("DELETE", "/api/teams/navy", nil, 404, "team_not_found")
	hub.mustJSON("GET", "/api/teams", nil, &list)
	if len(list) != 1 || list[0].Name != "red" {
		t.Errorf("teams = %+v, want red alone", list)
	}
}

// An upgraded hub keeps the shape it had: the loops in the fleet channel
// are the team named default, a loop outside it is in no team, and no
// loop's prompt changes, so its session goes on (ADR-0066).
func TestTheUpgradeMakesTheFleetChannelTheDefaultTeam(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	hub := startServer(t, dataDir)
	ws := workspaceWithScript(t, "!sysprompt\n")
	hub.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	hub.createLoop("briar", nil)
	hub.createLoop("cedar", map[string]any{"in_fleet_channel": false})
	at := time.Now().UnixMilli()
	hub.message("aster", "before")
	before := waitPrompt(t, hub, "aster", at)
	hub.stop()

	unmigrateTeams(t, dataDir)
	hub = startServer(t, dataDir)

	var list []teamJSON
	hub.mustJSON("GET", "/api/teams", nil, &list)
	if len(list) != 1 || list[0].Name != "default" || list[0].Description != "" || !reflect.DeepEqual(list[0].Loops, []string{"aster", "briar"}) {
		t.Fatalf("after the upgrade the teams are %+v, want default holding aster and briar", list)
	}
	if team := hub.loop("cedar").Team; team != "" {
		t.Errorf("cedar, outside the fleet channel, is in team %q after the upgrade", team)
	}
	at = time.Now().UnixMilli()
	hub.message("aster", "after")
	after := waitPrompt(t, hub, "aster", at)
	if after.SessionID != before.SessionID || after.ResultText != before.ResultText {
		t.Errorf("the upgrade changed aster's prompt (session %s -> %s):\nbefore:\n%s\nafter:\n%s",
			before.SessionID, after.SessionID, before.ResultText, after.ResultText)
	}
	// and the team it made is an ordinary one
	hub.mustJSON("PATCH", "/api/teams/default", map[string]any{"name": "crew"}, nil)
	if team := hub.loop("aster").Team; team != "crew" {
		t.Errorf("aster after default's rename reads team %q, want crew", team)
	}
}

// unmigrateTeams returns a stopped hub's database to its shape before
// teams, so the next start runs the shipped migration over its loops.
func unmigrateTeams(t *testing.T, dataDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range append(undo0079,
		`DROP INDEX loops_team`,
		`ALTER TABLE loops DROP COLUMN team`,
		`DROP TABLE teams`,
		`DELETE FROM schema_migrations WHERE version='0078_teams.sql'`,
	) {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// undo0079 takes a stopped hub's database back to before team channels; a
// test unmigrating an earlier migration runs it first, so the next start
// replays 0079 over the shape that test rebuilt. What came after 0079 goes
// first.
var undo0079 = append(slices.Clip(undo0081),
	`DROP INDEX channels_team`,
	`ALTER TABLE channels DROP COLUMN team`,
	`ALTER TABLE teams DROP COLUMN home_channel`,
	`DELETE FROM schema_migrations WHERE version='0079_team_channels.sql'`,
)

// undo0081 takes a database back to before a team named its Claude login.
var undo0081 = []string{
	`ALTER TABLE teams DROP COLUMN claude_login`,
	`DELETE FROM schema_migrations WHERE version='0081_team_claude_login.sql'`,
}
