package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func teamLoops(t *testing.T, db *DB, name string) []string {
	t.Helper()
	team, err := db.Teams().Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return team.LoopIDs
}

// loopTeam is the team a loop reads as in.
func loopTeam(t *testing.T, db *DB, loopID string) string {
	t.Helper()
	loopRecord, err := db.Loops().Get(context.Background(), loopID)
	if err != nil {
		t.Fatal(err)
	}
	return loopRecord.Team
}

func putInTeam(t *testing.T, db *DB, loopID, team string) error {
	t.Helper()
	_, err := db.Loops().Edit(context.Background(), loopID, store.LoopEdit{Team: &team, UpdatedAt: 5})
	return err
}

// A loop is in at most one team: putting it in another moves it, "" takes
// it out, and a team that does not exist is refused rather than made.
func TestALoopIsInOneTeamAtMost(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	for _, name := range []string{"red", "blue"} {
		if err := db.Teams().Create(ctx, &store.Team{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", CreatedAt: 2}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("a second team named red: %v, want ErrDuplicate", err)
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	if err := putInTeam(t, db, "l1", "blue"); err != nil {
		t.Fatal(err)
	}
	if got, want := teamLoops(t, db, "red"), []string{}; !reflect.DeepEqual(got, want) {
		t.Errorf("red after l1 moved to blue = %v, want %v", got, want)
	}
	if got := teamLoops(t, db, "blue"); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("blue = %v, want l1", got)
	}
	if err := putInTeam(t, db, "l2", "green"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("putting a loop in a team that doesn't exist: %v, want ErrNotFound", err)
	}
	if err := putInTeam(t, db, "l1", ""); err != nil {
		t.Fatal(err)
	}
	if team := loopTeam(t, db, "l1"); team != "" {
		t.Errorf("l1 taken out of its team reads team %q, want none", team)
	}

	created := testLoop()
	created.ID, created.Name, created.Team = "l3", "l3", "red"
	if err := db.Loops().Create(ctx, created); err != nil {
		t.Fatal(err)
	}
	if got := teamLoops(t, db, "red"); !reflect.DeepEqual(got, []string{"l3"}) {
		t.Errorf("red after a loop was created in it = %v, want l3", got)
	}
	orphan := testLoop()
	orphan.ID, orphan.Name, orphan.Team = "l4", "l4", "green"
	if err := db.Loops().Create(ctx, orphan); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("creating a loop in a team that doesn't exist: %v, want ErrNotFound", err)
	}
}

// A rename takes the team's loops with it, and a team with loops in it
// is not deleted.
func TestATeamKeepsItsLoopsThroughARename(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	for _, name := range []string{"red", "blue"} {
		if err := db.Teams().Create(ctx, &store.Team{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	taken := "blue"
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Name: &taken}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("renaming red to blue: %v, want ErrDuplicate", err)
	}
	newName, description := "crimson", "the red product"
	team, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Name: &newName, Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	if team.Name != "crimson" || team.Description != description || !reflect.DeepEqual(team.LoopIDs, []string{"l1"}) {
		t.Errorf("after the rename the team is %+v, want crimson with l1", team)
	}
	if team := loopTeam(t, db, "l1"); team != "crimson" {
		t.Errorf("l1 after its team's rename reads team %q, want crimson", team)
	}
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Description: &description}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("editing the old name: %v, want ErrNotFound", err)
	}

	if err := db.Teams().Delete(ctx, "crimson"); !errors.Is(err, store.ErrTeamHasLoops) {
		t.Errorf("deleting a team with a loop in it: %v, want ErrTeamHasLoops", err)
	}
	if err := putInTeam(t, db, "l1", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Delete(ctx, "crimson"); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Delete(ctx, "crimson"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
	teams, err := db.Teams().List(ctx)
	if err != nil || len(teams) != 1 || teams[0].Name != "blue" {
		t.Errorf("teams = %+v (%v), want blue alone", teams, err)
	}
	// a loop goes whatever team it is in
	if err := putInTeam(t, db, "l1", "blue"); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1", 9); err != nil {
		t.Fatal(err)
	}
}

func unmigrate0078(db *DB) error {
	for _, statement := range []string{
		`DROP INDEX loops_team`,
		`ALTER TABLE loops DROP COLUMN team`,
		`DROP TABLE teams`,
		`DELETE FROM schema_migrations WHERE version='0078_teams.sql'`,
	} {
		if _, err := db.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// The upgrade puts the fleet channel's loops in a team named default and
// leaves the rest in none; a hub with no loop in the fleet channel, or a
// fresh one, gets no team (ADR-0066).
func TestTheUpgradeMakesTheFleetChannelTheDefaultTeam(t *testing.T) {
	ctx := context.Background()
	fresh := openTestDB(t)
	if teams, err := fresh.Teams().List(ctx); err != nil || len(teams) != 0 {
		t.Errorf("a fresh hub has teams %+v (%v), want none", teams, err)
	}

	for _, test := range []struct {
		name      string
		outside   []string
		wantTeams int
	}{
		{"some loops in group", []string{"l2"}, 1},
		{"no loop in group", []string{"l1", "l2"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openTestDB(t)
			createLoops(t, db, "l1", "l2")
			outside := true
			for _, id := range test.outside {
				if _, err := db.Loops().Edit(ctx, id, store.LoopEdit{OutsideFleetChannel: &outside, UpdatedAt: 5}); err != nil {
					t.Fatal(err)
				}
			}
			if err := unmigrate0078(db); err != nil {
				t.Fatal(err)
			}
			if err := db.migrate(); err != nil {
				t.Fatal(err)
			}
			teams, err := db.Teams().List(ctx)
			if err != nil || len(teams) != test.wantTeams {
				t.Fatalf("after the upgrade the teams are %+v (%v), want %d", teams, err, test.wantTeams)
			}
			if test.wantTeams == 1 {
				if team := teams[0]; team.Name != "default" || team.Description != "" || !reflect.DeepEqual(team.LoopIDs, []string{"l1"}) {
					t.Errorf("the upgrade's team is %+v, want default holding l1 alone", team)
				}
			}
			for _, id := range test.outside {
				if team := loopTeam(t, db, id); team != "" {
					t.Errorf("%s, outside group, is in team %q, want none", id, team)
				}
			}
		})
	}
}
