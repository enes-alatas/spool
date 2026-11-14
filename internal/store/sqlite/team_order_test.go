package sqlite

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func teamOrderOf(t *testing.T, db *DB) []string {
	t.Helper()
	order, err := db.Teams().Order(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func makeTeams(t *testing.T, db *DB, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := db.Teams().Create(context.Background(), &store.Team{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func listedTeams(t *testing.T, db *DB) []string {
	t.Helper()
	teams, err := db.Teams().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, team := range teams {
		names = append(names, team.Name)
	}
	return names
}

// An order nobody set puts No team last. A set order is every reader's,
// and the list of teams comes in it too (#1451).
func TestTheHubKeepsOneOrderOfItsTeams(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if got := teamOrderOf(t, db); !slices.Equal(got, []string{""}) {
		t.Fatalf("a hub with no teams orders %q, want No team alone", got)
	}
	makeTeams(t, db, "red", "blue")
	if got, want := teamOrderOf(t, db), []string{"red", "blue", ""}; !slices.Equal(got, want) {
		t.Fatalf("before any order is set: %q, want %q", got, want)
	}

	if err := db.Teams().SetOrder(ctx, []string{"blue", "", "red"}); err != nil {
		t.Fatal(err)
	}
	if got, want := teamOrderOf(t, db), []string{"blue", "", "red"}; !slices.Equal(got, want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
	if got, want := listedTeams(t, db), []string{"blue", "red"}; !slices.Equal(got, want) {
		t.Fatalf("List = %q, want the order without No team, %q", got, want)
	}

	// No team not last: a new team goes last.
	makeTeams(t, db, "green")
	if got, want := teamOrderOf(t, db), []string{"blue", "", "red", "green"}; !slices.Equal(got, want) {
		t.Fatalf("after a new team: %q, want %q", got, want)
	}
	// No team last: a new team goes before it.
	if err := db.Teams().SetOrder(ctx, []string{"green", "red", "blue", ""}); err != nil {
		t.Fatal(err)
	}
	makeTeams(t, db, "amber")
	if got, want := teamOrderOf(t, db), []string{"green", "red", "blue", "amber", ""}; !slices.Equal(got, want) {
		t.Fatalf("after a new team with No team last: %q, want %q", got, want)
	}

	// A rename keeps the place, a delete leaves every other one.
	newName := "crimson"
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Delete(ctx, "blue"); err != nil {
		t.Fatal(err)
	}
	if got, want := teamOrderOf(t, db), []string{"green", "crimson", "amber", ""}; !slices.Equal(got, want) {
		t.Fatalf("after a rename and a delete: %q, want %q", got, want)
	}
	makeTeams(t, db, "teal")
	if got, want := teamOrderOf(t, db), []string{"green", "crimson", "amber", "teal", ""}; !slices.Equal(got, want) {
		t.Fatalf("a team made after a delete: %q, want %q", got, want)
	}
}

// A team made from a channel takes its place in the order as any new team
// does (ADR-0068).
func TestATeamFromAChannelTakesTheLastPlace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	makeTeams(t, db, "red")
	if err := db.Teams().SetOrder(ctx, []string{"red", ""}); err != nil {
		t.Fatal(err)
	}
	if err := db.Channels().Create(ctx, &store.Channel{Name: "ops", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().CreateFromChannel(ctx, &store.Team{Name: "ops", HomeChannel: "ops", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if got, want := teamOrderOf(t, db), []string{"red", "ops", ""}; !slices.Equal(got, want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

// An order that is not exactly the hub's teams and No team is refused, and
// changes nothing.
func TestAStaleTeamOrderIsRefused(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	makeTeams(t, db, "red", "blue")
	for _, order := range [][]string{
		{"red", ""},                  // leaves blue out
		{"red", "blue", "green", ""}, // names a team the hub doesn't have
		{"red", "green", ""},         // the right length, a wrong name
		{"red", "red", ""},           // a team twice
		{"red", "blue"},              // no place for No team
		{"red", "blue", "", ""},      // No team twice
	} {
		if err := db.Teams().SetOrder(ctx, order); !errors.Is(err, store.ErrTeamOrderStale) {
			t.Errorf("SetOrder(%q) = %v, want ErrTeamOrderStale", order, err)
		}
	}
	if got, want := teamOrderOf(t, db), []string{"red", "blue", ""}; !slices.Equal(got, want) {
		t.Fatalf("a refused order changed it: %q, want %q", got, want)
	}
}
