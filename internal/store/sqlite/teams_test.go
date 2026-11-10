package sqlite

import (
	"context"
	"errors"
	"reflect"
	"slices"
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

// A team's channels hold only its loops: a loop joins the home channel
// with the team, and leaving the team takes it out of every channel of the
// team and unbinds its rooms there, while its hub channels stay.
func TestATeamsChannelsKeepToItsLoops(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", HomeChannel: "red", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []*store.Channel{{Name: "red-side", Team: "red", CreatedAt: 1}, {Name: "hub", CreatedAt: 1}} {
		if err := db.Channels().Create(ctx, channel); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Channels().Create(ctx, &store.Channel{Name: "nowhere", Team: "green", CreatedAt: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a channel for a team that doesn't exist: %v, want ErrNotFound", err)
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, "red"); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("the home channel after l1 joined red = %v, want l1", got)
	}
	if err := db.Channels().AddLoop(ctx, "red-side", "l2", 2); !errors.Is(err, store.ErrChannelInOtherTeam) {
		t.Errorf("a loop in no team put in red's channel: %v, want ErrChannelInOtherTeam", err)
	}
	// a channel deleted between AddLoop's lookup and its insert is gone, not another team's
	if err := (channels{db.db}).addLoop(ctx, "deleted-meanwhile", "l1", 2); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a loop put in a channel deleted meanwhile: %v, want ErrNotFound", err)
	}
	for _, channel := range []string{"red-side", "hub"} {
		if err := db.Channels().AddLoop(ctx, channel, "l1", 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-100", "red-side", 3); err != nil {
		t.Fatal(err)
	}

	if err := putInTeam(t, db, "l1", ""); err != nil {
		t.Fatal(err)
	}
	for channel, want := range map[string][]string{"red": {}, "red-side": {}, "hub": {"l1"}} {
		if got := members(t, db, channel); !reflect.DeepEqual(got, want) {
			t.Errorf("%s after l1 left red = %v, want %v", channel, got, want)
		}
	}
	rooms, err := db.Rooms().List(ctx, "l1")
	if err != nil || len(rooms) != 1 || rooms[0].Channel != "" {
		t.Errorf("l1's rooms after it left red = %+v (%v), want its red-side room unbound", rooms, err)
	}

	// a loop created in the team starts in its home channel
	created := testLoop()
	created.ID, created.Name, created.Team = "l3", "l3", "red"
	if err := db.Loops().Create(ctx, created); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, "red"); !reflect.DeepEqual(got, []string{"l3"}) {
		t.Errorf("the home channel after l3 was created in red = %v, want l3", got)
	}
}

// A channel goes to a team only with no outsider in it, a team's home
// stays its own until the team names another, and a home channel is one of
// the team's; the team's channels go with it.
func TestAChannelChangesTeamOnlyWhole(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	for _, team := range []*store.Team{{Name: "red", HomeChannel: "red", CreatedAt: 1}, {Name: "blue", CreatedAt: 1}} {
		if err := db.Teams().Create(ctx, team); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Teams().Create(ctx, &store.Team{Name: "teal", HomeChannel: "red", CreatedAt: 1}); !errors.Is(err, store.ErrChannelTaken) {
		t.Errorf("a team whose home channel's name is taken: %v, want ErrChannelTaken", err)
	}
	if _, err := db.Teams().Get(ctx, "teal"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the refused team was kept: %v", err)
	}
	if err := db.Channels().Create(ctx, &store.Channel{Name: "ops", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"l1", "l2"} {
		if err := db.Channels().AddLoop(ctx, "ops", id, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Channels().SetTeam(ctx, "ops", "red"); !errors.Is(err, store.ErrChannelHasOutsiders) {
		t.Errorf("giving red a channel l2 is in: %v, want ErrChannelHasOutsiders", err)
	}
	if err := db.Channels().RemoveLoop(ctx, "ops", "l2", 3); err != nil {
		t.Fatal(err)
	}
	if channel, err := db.Channels().SetTeam(ctx, "ops", "red"); err != nil || channel.Team != "red" {
		t.Fatalf("giving red ops = %+v (%v)", channel, err)
	}
	if _, err := db.Channels().SetTeam(ctx, "red", ""); !errors.Is(err, store.ErrChannelIsHome) {
		t.Errorf("taking red's home channel from it: %v, want ErrChannelIsHome", err)
	}
	if _, err := db.Channels().SetTeam(ctx, "ops", "green"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("giving a channel to a team that doesn't exist: %v, want ErrNotFound", err)
	}

	// blue can't take red's channel as its home
	ops := "ops"
	if _, err := db.Teams().Edit(ctx, "blue", store.TeamEdit{HomeChannel: &ops, At: 4}); !errors.Is(err, store.ErrChannelInOtherTeam) {
		t.Errorf("blue's home set to red's channel: %v, want ErrChannelInOtherTeam", err)
	}
	// red's new home takes every loop of red in
	if err := db.Channels().RemoveLoop(ctx, "ops", "l1", 4); err != nil {
		t.Fatal(err)
	}
	team, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &ops, At: 5})
	if err != nil || team.HomeChannel != "ops" || !reflect.DeepEqual(team.Channels, []string{"ops", "red"}) {
		t.Fatalf("red with ops as its home = %+v (%v)", team, err)
	}
	if got := members(t, db, "ops"); !reflect.DeepEqual(got, []string{"l1"}) {
		t.Errorf("ops as red's home holds %v, want l1", got)
	}
	// naming the home it has again doesn't put back a loop taken out
	if err := db.Channels().RemoveLoop(ctx, "ops", "l1", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &ops, At: 7}); err != nil {
		t.Fatal(err)
	}
	if got := members(t, db, "ops"); len(got) != 0 {
		t.Errorf("naming red's own home again put back %v", got)
	}
	// a rename carries the channels along
	crimson := "crimson"
	if team, err := db.Teams().Edit(ctx, "red", store.TeamEdit{Name: &crimson}); err != nil || !reflect.DeepEqual(team.Channels, []string{"ops", "red"}) {
		t.Fatalf("crimson = %+v (%v), want red's channels", team, err)
	}
	if channel, err := db.Channels().Get(ctx, "ops"); err != nil || channel.Team != "crimson" {
		t.Errorf("ops after red's rename = %+v (%v), want crimson's", channel, err)
	}

	// deleting the team deletes its channels, and only its own
	if err := putInTeam(t, db, "l1", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Delete(ctx, "crimson"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ops", "red"} {
		if _, err := db.Channels().Get(ctx, name); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s outlived its team: %v", name, err)
		}
	}
}

// A hub channel named as a team's home is given to the team in the same
// write, members and all, while every loop in it is the team's; one that
// holds an outsider stays a hub channel (ADR-0068).
func TestAHubChannelBecomesATeamsHome(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1", "l2")
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	if err := db.Channels().Create(ctx, &store.Channel{Name: "fleet", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"l1", "l2"} {
		if err := db.Channels().AddLoop(ctx, "fleet", id, 3); err != nil {
			t.Fatal(err)
		}
	}
	fleet := "fleet"
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &fleet, At: 4}); !errors.Is(err, store.ErrChannelHasOutsiders) {
		t.Fatalf("red's home set to a hub channel holding l2: %v, want ErrChannelHasOutsiders", err)
	}
	if channel, err := db.Channels().Get(ctx, "fleet"); err != nil || channel.Team != "" {
		t.Fatalf("the refused home moved fleet: %+v (%v)", channel, err)
	}
	if err := db.Channels().RemoveLoop(ctx, "fleet", "l2", 5); err != nil {
		t.Fatal(err)
	}
	team, err := db.Teams().Edit(ctx, "red", store.TeamEdit{HomeChannel: &fleet, At: 6})
	if err != nil || team.HomeChannel != "fleet" || !slices.Contains(team.Channels, "fleet") {
		t.Fatalf("red with fleet as its home = %+v (%v)", team, err)
	}
	if channel, err := db.Channels().Get(ctx, "fleet"); err != nil || channel.Team != "red" || !reflect.DeepEqual(channel.LoopIDs, []string{"l1"}) {
		t.Errorf("fleet as red's home = %+v (%v), want red's, holding l1", channel, err)
	}
}

// A team names a Claude login its loops read with them; a login the hub
// doesn't hold is refused, "" clears it, and a login a team names can't
// be deleted (ADR-0066).
func TestATeamNamesItsClaudeLogin(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createLoops(t, db, "l1")
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "ada", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.Teams().Create(ctx, &store.Team{Name: "ghost", ClaudeLogin: "nobody", CreatedAt: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a team made on a login the hub doesn't hold: %v, want ErrNotFound", err)
	}
	if err := db.Teams().Create(ctx, &store.Team{Name: "red", ClaudeLogin: "ada", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := putInTeam(t, db, "l1", "red"); err != nil {
		t.Fatal(err)
	}
	if loopRecord, err := db.Loops().Get(ctx, "l1"); err != nil || loopRecord.TeamClaudeLogin != "ada" {
		t.Errorf("l1 in red reads its team's login as %+v, %v; want ada", loopRecord, err)
	}
	if err := db.ClaudeLogins().Delete(ctx, "ada"); !errors.Is(err, store.ErrLoginNamedByTeam) {
		t.Errorf("deleting a login red names: %v, want ErrLoginNamedByTeam", err)
	}

	nobody, none := "nobody", ""
	if _, err := db.Teams().Edit(ctx, "red", store.TeamEdit{ClaudeLogin: &nobody}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("red put on a login the hub doesn't hold: %v, want ErrNotFound", err)
	}
	red, err := db.Teams().Edit(ctx, "red", store.TeamEdit{ClaudeLogin: &none})
	if err != nil || red.ClaudeLogin != "" {
		t.Fatalf("red with its login cleared = %+v, %v", red, err)
	}
	if loopRecord, _ := db.Loops().Get(ctx, "l1"); loopRecord.TeamClaudeLogin != "" {
		t.Errorf("l1 still reads red's cleared login as %q", loopRecord.TeamClaudeLogin)
	}
	if err := db.ClaudeLogins().Delete(ctx, "ada"); err != nil {
		t.Errorf("deleting ada once no team names it: %v", err)
	}
}
