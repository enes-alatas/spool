package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// failingChannels is a channel store whose description write fails.
type failingChannels struct {
	store.ChannelStore
	failDescribe bool
}

func (channels failingChannels) SetDescription(ctx context.Context, name, description string) (*store.Channel, error) {
	if channels.failDescribe {
		return nil, errors.New("disk full")
	}
	return channels.ChannelStore.SetDescription(ctx, name, description)
}

// failingTeams is a team store whose create fails.
type failingTeams struct {
	store.TeamStore
	failCreate bool
}

func (teams failingTeams) Create(ctx context.Context, team *store.Team) error {
	if teams.failCreate {
		return errors.New("disk full")
	}
	return teams.TeamStore.Create(ctx, team)
}

// teamFailing is a store whose channels and teams fail as they say.
type teamFailing struct {
	store.Store
	channels failingChannels
	teams    failingTeams
}

func (failing teamFailing) Channels() store.ChannelStore { return failing.channels }
func (failing teamFailing) Teams() store.TeamStore       { return failing.teams }

// undeletableLoops is a loop store whose removals fail.
type undeletableLoops struct{ store.LoopStore }

func (undeletableLoops) Delete(context.Context, string, int64) error {
	return errors.New("disk full")
}

// loopsUndeletable is a store whose loops can't be removed again.
type loopsUndeletable struct{ store.Store }

func (failing loopsUndeletable) Loops() store.LoopStore {
	return undeletableLoops{failing.Store.Loops()}
}

func crewTeam() *store.Team {
	return &store.Team{Name: "crew", Description: "A crew.", HomeChannel: "crew"}
}

// TestATeamThatCannotBeUndoneNamesWhatIsLeft: when removing a stored
// loop fails too, the refusal names each loop left and the team holding
// them, for the operator.
func TestATeamThatCannotBeUndoneNamesWhatIsLeft(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Loops().Create(ctx, teamLoop("other", "qa")); err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: loopsUndeletable{db}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	refusal := server.storeTeam(ctx, crewTeam(), teamLoops("planner", "builder", "qa"))
	if refusal == nil || refusal.status != 409 {
		t.Fatalf("refusal %+v; want status 409", refusal)
	}
	for _, left := range []string{"the loop planner", "the loop builder", "the team crew", "delete them"} {
		if !strings.Contains(refusal.msg, left) {
			t.Errorf("refusal %q; want it to name %q", refusal.msg, left)
		}
	}
}

func teamLoops(names ...string) []*store.Loop {
	var loops []*store.Loop
	for _, name := range names {
		loops = append(loops, teamLoop("id-"+name, name))
	}
	return loops
}

// TestATeamThatFailsToStoreLeavesNothing: a team or channel name taken,
// a team that can't be created, a description that can't be written and
// a loop name taken while the import ran each answer a refusal and leave
// no team, channel or loop of the import; a team that stores leaves all
// of them, every loop in the team and its home channel.
func TestATeamThatFailsToStoreLeavesNothing(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, test := range []struct {
		name               string
		channels           failingChannels
		teams              failingTeams
		loopTaken, team    string
		channelTaken, code string
		status             int
	}{
		{name: "a team of that name", team: "crew", status: 409, code: codeTeamExists},
		{name: "a channel of that name", channelTaken: "crew", status: 409, code: codeChannelExists},
		{name: "the team fails", teams: failingTeams{failCreate: true}, status: 500},
		{name: "the description fails", channels: failingChannels{failDescribe: true}, status: 500},
		{name: "a name taken meanwhile", loopTaken: "qa", status: 409},
	} {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if test.loopTaken != "" {
			if err := db.Loops().Create(ctx, teamLoop("other", test.loopTaken)); err != nil {
				t.Fatal(err)
			}
		}
		if test.team != "" {
			if err := db.Teams().Create(ctx, &store.Team{Name: test.team, HomeChannel: "elsewhere", CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if test.channelTaken != "" {
			if err := db.Channels().Create(ctx, &store.Channel{Name: test.channelTaken, CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
		}
		test.channels.ChannelStore, test.teams.TeamStore = db.Channels(), db.Teams()
		server := &Server{Store: teamFailing{Store: db, channels: test.channels, teams: test.teams}, Log: quiet}
		refusal := server.storeTeam(ctx, crewTeam(), teamLoops("planner", "builder", "qa"))
		if refusal == nil || refusal.status != test.status || refusal.code != test.code {
			t.Errorf("%s: refusal %+v; want status %d, code %q", test.name, refusal, test.status, test.code)
		}
		for _, name := range []string{"planner", "builder"} {
			if _, err := db.Loops().GetByName(ctx, name); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: loop %s after the import failed: err %v; want it gone", test.name, name, err)
			}
		}
		if test.team == "" {
			if _, err := db.Teams().Get(ctx, "crew"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: the team after the import failed: err %v; want it gone", test.name, err)
			}
		}
		if test.channelTaken == "" {
			if _, err := db.Channels().Get(ctx, "crew"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: the channel after the import failed: err %v; want it gone", test.name, err)
			}
		}
	}

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := &Server{Store: db, Log: quiet}
	if refusal := server.storeTeam(ctx, crewTeam(), teamLoops("planner", "builder", "qa")); refusal != nil {
		t.Fatalf("storing the team: %+v", refusal)
	}
	team, err := db.Teams().Get(ctx, "crew")
	if err != nil || len(team.LoopIDs) != 3 || team.HomeChannel != "crew" || team.Description != "A crew." {
		t.Errorf("the team stored: %+v, %v; want crew with the three loops, at home in crew", team, err)
	}
	stored, err := db.Channels().Get(ctx, "crew")
	if err != nil || len(stored.LoopIDs) != 3 || stored.Description != "A crew." || stored.Team != "crew" {
		t.Errorf("the channel stored: %+v, %v; want crew's, with the three loops and the description", stored, err)
	}
}

// teamLoop is a bare loop the store takes.
func teamLoop(id, name string) *store.Loop {
	return &store.Loop{
		ID: id, Name: name, Mission: "m", Runtime: store.RuntimeBare, Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Subagents: store.SubagentsAllowed,
	}
}
