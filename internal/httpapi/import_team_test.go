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

// failingChannels is a channel store whose create or add fails.
type failingChannels struct {
	store.ChannelStore
	failCreate, failAdd bool
}

func (channels failingChannels) Create(ctx context.Context, channel *store.Channel) error {
	if channels.failCreate {
		return errors.New("disk full")
	}
	return channels.ChannelStore.Create(ctx, channel)
}

func (channels failingChannels) AddLoop(ctx context.Context, name, loopID string, at int64) error {
	if channels.failAdd {
		return errors.New("disk full")
	}
	return channels.ChannelStore.AddLoop(ctx, name, loopID, at)
}

// channelsFailing is a store whose channels fail as channels says.
type channelsFailing struct {
	store.Store
	channels failingChannels
}

func (failing channelsFailing) Channels() store.ChannelStore { return failing.channels }

// undeletableLoops is a loop store whose removals fail.
type undeletableLoops struct{ store.LoopStore }

func (undeletableLoops) Delete(context.Context, string, int64) error {
	return errors.New("disk full")
}

// loopsUndeletable is a store whose channels can't take a member and
// whose loops can't be removed again.
type loopsUndeletable struct{ channelsFailing }

func (failing loopsUndeletable) Loops() store.LoopStore {
	return undeletableLoops{failing.channelsFailing.Loops()}
}

// TestATeamThatCannotBeUndoneNamesWhatIsLeft: when removing a stored
// loop fails too, the refusal names each loop left for the operator.
func TestATeamThatCannotBeUndoneNamesWhatIsLeft(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	failing := loopsUndeletable{channelsFailing{Store: db, channels: failingChannels{ChannelStore: db.Channels(), failAdd: true}}}
	server := &Server{Store: failing, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	refusal := server.storeTeam(ctx, &store.Channel{Name: "crew"}, teamLoops("planner", "builder"))
	if refusal == nil || refusal.status != 500 {
		t.Fatalf("refusal %+v; want status 500", refusal)
	}
	for _, left := range []string{"the loop planner", "the loop builder", "delete them"} {
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

// TestATeamThatFailsToStoreLeavesNothing: a loop name taken while the
// import ran, a channel that can't be created and a loop that can't be
// put in it each answer a refusal and leave none of the team's loops and
// no channel; a team that stores leaves all of them, in the channel.
func TestATeamThatFailsToStoreLeavesNothing(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	team := func() *store.Channel { return &store.Channel{Name: "crew", Description: "A crew."} }
	for _, test := range []struct {
		name     string
		channels failingChannels
		taken    string
		status   int
	}{
		{name: "a name taken meanwhile", taken: "qa", status: 409},
		{name: "the channel fails", channels: failingChannels{failCreate: true}, status: 500},
		{name: "a member fails", channels: failingChannels{failAdd: true}, status: 500},
	} {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if test.taken != "" {
			if err := db.Loops().Create(ctx, teamLoop("other", test.taken)); err != nil {
				t.Fatal(err)
			}
		}
		test.channels.ChannelStore = db.Channels()
		server := &Server{Store: channelsFailing{Store: db, channels: test.channels}, Log: quiet}
		refusal := server.storeTeam(ctx, team(), teamLoops("planner", "builder", "qa"))
		if refusal == nil || refusal.status != test.status {
			t.Errorf("%s: refusal %+v; want status %d", test.name, refusal, test.status)
		}
		for _, name := range []string{"planner", "builder"} {
			if _, err := db.Loops().GetByName(ctx, name); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: loop %s after the import failed: err %v; want it gone", test.name, name, err)
			}
		}
		if _, err := db.Channels().Get(ctx, "crew"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: the channel after the import failed: err %v; want it gone", test.name, err)
		}
	}

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := &Server{Store: db, Log: quiet}
	channel := team()
	refusal := server.storeTeam(ctx, channel, teamLoops("planner", "builder", "qa"))
	if refusal != nil {
		t.Fatalf("storing the team: %+v", refusal)
	}
	stored, err := db.Channels().Get(ctx, channel.Name)
	if err != nil || len(stored.LoopIDs) != 3 || stored.Description != "A crew." {
		t.Errorf("the channel stored: %+v, %v; want crew with the three loops", stored, err)
	}
}

// teamLoop is a bare loop the store takes.
func teamLoop(id, name string) *store.Loop {
	return &store.Loop{
		ID: id, Name: name, Mission: "m", Runtime: store.RuntimeBare, Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Subagents: store.SubagentsAllowed,
	}
}
