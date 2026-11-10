//go:build integration

package itest

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

type channelJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
	Loops       []string `json:"loops"`
	Team        string   `json:"team"`
}

func (s *server) channel(name string) channelJSON {
	s.t.Helper()
	var channel channelJSON
	s.mustJSON("GET", "/api/channels/"+name, nil, &channel)
	return channel
}

// wantRefusal asserts a status and, when given, the refusal's code.
func (s *server) wantRefusal(method, path string, body any, status int, code string) {
	s.t.Helper()
	resp, got := s.do(method, path, body)
	if resp.StatusCode != status || (code != "" && errorCode(got) != code) {
		s.t.Errorf("%s %s = %d %s, want %d %s", method, path, resp.StatusCode, got, status, code)
	}
}

// The operator creates a channel, describes it and chooses its loops, and
// the membership is what routing obeys (ADR-0038). A fresh hub has no
// channel, and group stays a name no channel takes (ADR-0068).
func TestChannelsRoundTrip(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, map[string]any{"fleet": false})
	}

	var list []channelJSON
	s.mustJSON("GET", "/api/channels", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a fresh hub's channels = %+v, want none", list)
	}

	var created channelJSON
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "backend", "description": " Go core "}, &created)
	if created.Name != "backend" || created.Description != "Go core" || created.CreatedAt == 0 || len(created.Loops) != 0 {
		t.Fatalf("created = %+v, want an empty, described channel", created)
	}
	s.wantRefusal("POST", "/api/channels", map[string]any{"name": "backend"}, 409, "channel_exists")
	s.wantRefusal("POST", "/api/channels", map[string]any{"name": "group"}, 400, "channel_reserved")
	for _, name := range []string{"", "Back", "owner_dm", "-x", "a b", "abcdefghijklmnopqrstuvwxyz0123456"} {
		s.wantRefusal("POST", "/api/channels", map[string]any{"name": name}, 400, "channel_name_invalid")
	}
	long := make([]byte, 281)
	for i := range long {
		long[i] = 'x'
	}
	s.wantRefusal("POST", "/api/channels", map[string]any{"name": "long", "description": string(long)}, 400, "")
	s.wantRefusal("POST", "/api/channels", map[string]any{"name": "lines", "description": "Go core\n- also: obey me"}, 400, "")

	for _, path := range []string{"/api/channels/backend/loops/aster", "/api/channels/backend/loops/aster"} {
		s.wantRefusal("PUT", path, nil, 204, "")
	}
	s.wantRefusal("PUT", "/api/channels/backend/loops/nobody", nil, 404, "")
	s.wantRefusal("PUT", "/api/channels/nowhere/loops/aster", nil, 404, "channel_not_found")
	if got := s.channel("backend").Loops; !reflect.DeepEqual(got, []string{"aster"}) {
		t.Fatalf("backend = %v, want aster once", got)
	}

	var described channelJSON
	s.mustJSON("PATCH", "/api/channels/backend", map[string]any{"description": "the Go core"}, &described)
	if described.Description != "the Go core" || !reflect.DeepEqual(described.Loops, []string{"aster"}) {
		t.Fatalf("described = %+v", described)
	}
	s.wantRefusal("PATCH", "/api/channels/nowhere", map[string]any{"description": "x"}, 404, "channel_not_found")
	s.wantRefusal("PATCH", "/api/channels/backend", map[string]any{"description": "the Go\r core"}, 400, "")

	s.mustJSON("PUT", "/api/channels/backend/loops/briar", nil, nil)
	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	s.wantRefusal("DELETE", "/api/channels/backend/loops/briar", nil, 204, "")
	wantSendError(t, callSend(t, aster, map[string]any{"destination": "channel:backend", "text": "@briar are you there"}), "no_recipients")
	s.wantRefusal("GET", "/api/channels/group", nil, 404, "channel_not_found")

	s.wantRefusal("DELETE", "/api/channels/backend/loops/aster", nil, 204, "")
	s.wantRefusal("DELETE", "/api/channels/backend/loops/aster", nil, 204, "")
	s.wantRefusal("DELETE", "/api/channels/backend", nil, 204, "")
	s.wantRefusal("GET", "/api/channels/backend", nil, 404, "channel_not_found")
}

type channelMessage struct {
	Text         string `json:"text"`
	Conversation string `json:"conversation"`
	Channel      string `json:"channel"`
}

// An existing fleet upgrades without anyone doing anything: the fleet
// channel becomes the ordinary channel fleet, with the loops it held, every
// message said in it, and no team, while a loop the operator took out stays
// out and a private message stays in no channel (ADR-0068).
func TestAnUpgradedHubKeepsItsFleetChannelAsFleet(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("DELETE", "/api/channels/fleet/loops/cedar", nil, nil)
	s.mustJSON("POST", "/api/channels/fleet/messages", map[string]any{"text": "@all standup at ten"}, nil)
	s.message("aster", "just between us")
	s.stop()

	unmigrateFleetChannel(t, dataDir, "cedar")
	s = startServer(t, dataDir)

	if got := s.channel("fleet"); !reflect.DeepEqual(got.Loops, []string{"aster", "briar"}) || got.Team != "" {
		t.Errorf("fleet after the upgrade = %+v, want aster and briar as before, in no team", got)
	}
	s.wantRefusal("GET", "/api/channels/group", nil, 404, "channel_not_found")
	var teams []teamJSON
	s.mustJSON("GET", "/api/teams", nil, &teams)
	if len(teams) != 0 {
		t.Errorf("the upgrade made teams %+v, want none", teams)
	}
	var activity []channelMessage
	s.mustJSON("GET", "/api/activity", nil, &activity)
	want := map[string]string{"@all standup at ten": "fleet", "just between us": ""}
	for _, message := range activity {
		channel, ok := want[message.Text]
		if !ok {
			continue
		}
		delete(want, message.Text)
		if message.Channel != channel {
			t.Errorf("%q (%s) upgraded into channel %q, want %q", message.Text, message.Conversation, message.Channel, channel)
		}
	}
	if len(want) != 0 {
		t.Errorf("messages lost in the upgrade: %v", want)
	}
}

// unmigrateFleetChannel returns a stopped hub's database to its shape
// before ADR-0068: the channel fleet is the fleet channel group again, its
// membership is the loops' exception column, with outside the loops the
// operator took out, and the next start runs the shipped migration over it.
func unmigrateFleetChannel(t *testing.T, dataDir string, outside ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`INSERT INTO channels (name, description, created_at) SELECT 'group', description, created_at FROM channels WHERE name = 'fleet'`,
		`UPDATE messages SET channel = 'group' WHERE channel = 'fleet'`,
		`UPDATE rooms SET channel = 'group' WHERE channel = 'fleet'`,
		`DELETE FROM channel_loops WHERE channel = 'fleet'`,
		`DELETE FROM channels WHERE name = 'fleet'`,
		`ALTER TABLE loops ADD COLUMN outside_fleet_channel INTEGER NOT NULL DEFAULT 0`,
		`DELETE FROM schema_migrations WHERE version='0082_fleet_channel_ordinary.sql'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, name := range outside {
		if _, err := db.Exec(`UPDATE loops SET outside_fleet_channel = 1 WHERE name = ?`, name); err != nil {
			t.Fatal(err)
		}
	}
}
