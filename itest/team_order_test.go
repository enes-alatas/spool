//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

type teamOrderJSON struct {
	Order []string `json:"order"`
}

// teamOrderAs reads the hub's order of its teams with a session cookie.
func teamOrderAs(t *testing.T, hub *server, cookie string) []string {
	t.Helper()
	resp, out := hub.asCookie("GET", "/api/teams/order", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/teams/order = %d %s", resp.StatusCode, out)
	}
	var order teamOrderJSON
	if err := json.Unmarshal(out, &order); err != nil {
		t.Fatal(err)
	}
	return order.Order
}

// teamNamesAs reads the teams list, by name, with a session cookie.
func teamNamesAs(t *testing.T, hub *server, cookie string) []string {
	t.Helper()
	resp, out := hub.asCookie("GET", "/api/teams", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/teams = %d %s", resp.StatusCode, out)
	}
	var teams []teamJSON
	if err := json.Unmarshal(out, &teams); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, team := range teams {
		names = append(names, team.Name)
	}
	return names
}

// The hub keeps one order of its teams for everyone (#1451). An admin sets
// the whole of it, No team's place among the teams, and every user reads it
// so; a member can't set it. A new team goes last, before No team when No
// team is last; a deleted one leaves every other team's place as it was.
func TestTheHubKeepsOneOrderOfItsTeamsForEveryone(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"red", "blue", "green"} {
		hub.mustJSON("POST", "/api/teams", map[string]any{"name": name}, nil)
	}
	member := hub.signedInAs("mia", "member")
	if got, want := teamOrderAs(t, hub, member), []string{"red", "blue", "green", ""}; !slices.Equal(got, want) {
		t.Fatalf("before an order is set: %q, want the teams as made and No team last, %q", got, want)
	}

	// an admin sets it, and a member reads it so
	var set teamOrderJSON
	hub.mustJSON("PUT", "/api/teams/order", map[string]any{"order": []string{"green", "", "red", "blue"}}, &set)
	want := []string{"green", "", "red", "blue"}
	if !slices.Equal(set.Order, want) {
		t.Fatalf("PUT answered %q, want %q", set.Order, want)
	}
	if got := teamOrderAs(t, hub, member); !slices.Equal(got, want) {
		t.Fatalf("a member reads the order as %q, want %q", got, want)
	}
	if got := teamNamesAs(t, hub, member); !slices.Equal(got, []string{"green", "red", "blue"}) {
		t.Fatalf("a member's teams list = %q, want it in the order", got)
	}

	// a member can't set it, and the order stands
	if resp, out := hub.asCookie("PUT", "/api/teams/order", member,
		map[string]any{"order": []string{"", "red", "blue", "green"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member PUT /api/teams/order = %d %s, want 403", resp.StatusCode, out)
	}
	if got := teamOrderAs(t, hub, member); !slices.Equal(got, want) {
		t.Fatalf("a member's refused write changed the order to %q", got)
	}

	// a list that isn't the hub's teams is stale or invalid, and changes nothing
	hub.wantRefusal("PUT", "/api/teams/order", map[string]any{"order": []string{"green", "", "red"}}, 409, "team_order_stale")
	hub.wantRefusal("PUT", "/api/teams/order", map[string]any{"order": []string{"green", "", "red", "blue", "gold"}}, 409, "team_order_stale")
	hub.wantRefusal("PUT", "/api/teams/order", map[string]any{"order": []string{"green", "red", "red", "blue"}}, 400, "team_order_invalid")
	hub.wantRefusal("PUT", "/api/teams/order", map[string]any{"order": []string{"green", "red", "blue"}}, 400, "team_order_invalid")
	if got := teamOrderAs(t, hub, member); !slices.Equal(got, want) {
		t.Fatalf("a refused write changed the order to %q", got)
	}

	// with No team not last, a new team goes last; a delete leaves the rest
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "amber"}, nil)
	hub.mustJSON("DELETE", "/api/teams/red", nil, nil)
	if got, want := teamOrderAs(t, hub, member), []string{"green", "", "blue", "amber"}; !slices.Equal(got, want) {
		t.Fatalf("after a create and a delete: %q, want %q", got, want)
	}
	// with No team last, a new team goes before it
	hub.mustJSON("PUT", "/api/teams/order", map[string]any{"order": []string{"amber", "blue", "green", ""}}, nil)
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "teal"}, nil)
	if got, want := teamOrderAs(t, hub, member), []string{"amber", "blue", "green", "teal", ""}; !slices.Equal(got, want) {
		t.Fatalf("a new team with No team last: %q, want %q", got, want)
	}

	// no team takes the order route's name
	hub.wantRefusal("POST", "/api/teams", map[string]any{"name": "order"}, 400, "team_name_invalid")
}
