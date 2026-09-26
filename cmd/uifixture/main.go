// Command uifixture writes a synthetic Spool data directory for screenshots.
//
// A control-room screenshot in a PR is driven against fixture data, never the
// live fleet (CONVENTIONS.md): the timeline renders raw assistant text and
// tool inputs, so a live shot publishes whatever a loop happened to echo. The
// rule held, but the only documented way to take a shot was by hand against
// whatever the dev server pointed at — which is how the thirteen live-data
// images in the go-public audit were taken (#153, #248).
//
// So this writes the fleet the shots are of: four loops, two conversations,
// a spend history, a sender catalogue, secrets and fleet rules, all invented.
// Every name, handle, mission and figure here is made up; the one rule this
// file must keep is that nothing in it is copied from a real fleet, and no
// value in it is a real credential.
//
// It writes rows through the store interfaces rather than SQL, so a schema
// change breaks the build here instead of producing a fixture the room
// cannot read.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/datadir"
	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

func main() {
	dir := flag.String("data-dir", "", "directory to write spool.db into (required)")
	flag.Parse()
	if *dir == "" {
		log.Fatal("uifixture: --data-dir is required")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		log.Fatalf("uifixture: %v", err)
	}
	// A directory that already holds a store is someone's data, not a fixture
	// to write: seeding it would mix invented loops into a real fleet.
	dbPath := filepath.Join(*dir, "spool.db")
	if _, err := os.Stat(dbPath); err == nil {
		log.Fatalf("uifixture: %s already exists; give it a fresh directory", dbPath)
	}
	db, err := sqlite.Open(dbPath)
	if err != nil {
		log.Fatalf("uifixture: %v", err)
	}
	defer db.Close()
	if err := seed(context.Background(), db); err != nil {
		log.Fatalf("uifixture: %v", err)
	}
	if err := writeOperatorToken(*dir); err != nil {
		log.Fatalf("uifixture: %v", err)
	}
	fmt.Println(*dir)
}

// fixtureOperatorToken is the operator token of every hub started on a
// fixture directory. A hub reads an existing token file rather than minting
// one (operator.Load), so this is the value the login form, the session
// cookie and a failed smoke's public trace carry. Minted, it would be a
// random 64-hex string that a reader of that trace could only judge harmless
// by knowing the hub is gone; this one says what it is.
const fixtureOperatorToken = "uifixture-operator-token-not-a-secret"

// writeOperatorToken seeds that token. It never replaces one that is already
// there: pointed at a real data directory by mistake, a truncating write
// would swap that hub's credential for one published in this repo, and the
// next start would accept it.
func writeOperatorToken(dir string) error {
	f, err := os.OpenFile(operator.Path(dir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, datadir.FileMode)
	if err != nil {
		return fmt.Errorf("operator token: %w", err)
	}
	if _, err := f.WriteString(fixtureOperatorToken + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("operator token: %w", err)
	}
	return f.Close()
}

// The clock the fixture is written against. Times are relative to now so the
// room renders "4m ago" rather than a date two years old, which would read as
// a broken fleet rather than a working one.
var now = time.Now()

func ms(d time.Duration) int64 { return now.Add(d).UnixMilli() }

type fixtureLoop struct {
	name     string
	mission  string
	model    string
	runtime  string
	status   string
	effort   string
	pacing   string
	branch   string
	repoPath string
	botUser  string
}

// The fleet: one loop per shape the room draws differently — contained and
// uncontained, with a workspace and without, running and paused.
var loops = []fixtureLoop{
	{
		name:     "gardener",
		mission:  "Keep the docs garden tidy: read what landed this week, fix what has gone stale, and open a PR when a page no longer matches the code.",
		model:    "opus",
		runtime:  store.RuntimeDocker,
		status:   store.StatusActive,
		effort:   "high",
		pacing:   store.PacingSelf,
		branch:   "loop/gardener",
		repoPath: "/srv/example/handbook",
		botUser:  "gardener_example_bot",
	},
	{
		name:    "watcher",
		mission: "Watch the nightly build. When it breaks, say which commit broke it and what the first failing line was — no speculation.",
		model:   "sonnet",
		runtime: store.RuntimeDocker,
		status:  store.StatusActive,
		pacing:  store.PacingFixed,
		botUser: "watcher_example_bot",
	},
	{
		name:     "courier",
		mission:  "Answer questions about the release schedule from the shared calendar. Say you do not know rather than guessing a date.",
		model:    "claude-haiku-4-5-20251001",
		runtime:  store.RuntimeBare,
		status:   store.StatusActive,
		pacing:   store.PacingFixed,
		repoPath: "/srv/example/release-notes",
		branch:   "loop/courier",
	},
	{
		name:    "archivist",
		mission: "Summarise last quarter's incident reports into one page a new joiner can read in ten minutes.",
		model:   "sonnet",
		runtime: store.RuntimeDocker,
		status:  store.StatusPaused,
		pacing:  store.PacingFixed,
	},
}

func seed(ctx context.Context, db store.Store) error {
	ids := map[string]string{}
	for i, fl := range loops {
		l := &store.Loop{
			ID:              fmt.Sprintf("loop_fixture%02d", i+1),
			Name:            fl.name,
			Mission:         fl.mission,
			Model:           fl.model,
			Runtime:         fl.runtime,
			WorkspaceMode:   store.WorkspaceNone,
			Status:          fl.status,
			Effort:          fl.effort,
			Pacing:          fl.pacing,
			TickIntervalSec: 1800,
			MinWakeSec:      300,
			MaxWakeSec:      14400,
			IdleTimeoutSec:  900,
			TGBotUsername:   fl.botUser,
			CreatedAt:       ms(-21 * 24 * time.Hour),
			UpdatedAt:       ms(-4 * time.Minute),
		}
		if fl.repoPath != "" {
			l.WorkspaceMode = store.WorkspaceWorktree
			l.RepoPath = fl.repoPath
			l.WorktreePath = filepath.Join("/srv/example/worktrees", fl.name)
			l.WorkspacePath = l.WorktreePath
			l.Branch = fl.branch
		}
		if fl.runtime == store.RuntimeDocker {
			// The API fills these in on every contained loop it creates
			// (internal/httpapi/api.go), so a store where they are zero is
			// one no operator could have made — and the workstation panel
			// renders the zero as "0 MB · 0 cpu".
			l.MemMB = 4096
			l.CPUs = 2
		}
		if fl.botUser != "" {
			// A bot username with no token is a store no operator could have
			// made, and the loop page reads the token's presence, not the
			// name, to say a surface is attached (#287). Not token-shaped,
			// so the secret scan has nothing to weigh, and ui-shots points
			// the hub's Bot API at a closed local port, so no poller ever
			// carries it off the machine.
			l.TGBotToken = "uifixture-not-a-bot"
			l.TGGroupChatID = -1001000000000 - int64(i)
			l.TGGroupBoundAt = ms(-20 * 24 * time.Hour)
			l.OwnerTGUserID = 700000001
			l.OwnerDMChatID = 700000001
		}
		if err := db.Loops().Create(ctx, l); err != nil {
			return fmt.Errorf("create %s: %w", fl.name, err)
		}
		ids[fl.name] = l.ID
		next := ms(12 * time.Minute)
		if fl.status == store.StatusPaused {
			next = 0
		}
		if err := db.Schedule().Set(ctx, l.ID, next); err != nil {
			return fmt.Errorf("schedule %s: %w", fl.name, err)
		}
	}

	// A fixture fleet with no operator token renders as four broken loops
	// behind a red banner — true of that store, and not what a screenshot of
	// the control room is meant to show. The value is a synthetic one in the
	// shape `claude setup-token` prints; it is never used to reach anything,
	// because no loop in this store ever wakes.
	if err := db.Settings().Set(ctx, store.SettingClaudeOAuthToken,
		"sk-ant-oat01-"+strings.Repeat("0", 32)+"-uifixture"); err != nil {
		return fmt.Errorf("settings: %w", err)
	}

	if err := seedTimeline(ctx, db, ids["gardener"]); err != nil {
		return err
	}
	if err := seedSpend(ctx, db, ids); err != nil {
		return err
	}
	if err := seedCurrentSessions(ctx, db, ids); err != nil {
		return err
	}
	if err := seedConversations(ctx, db, ids); err != nil {
		return err
	}
	if err := seedSenders(ctx, db); err != nil {
		return err
	}
	if err := seedSecrets(ctx, db, ids["gardener"]); err != nil {
		return err
	}
	return seedRules(ctx, db)
}
