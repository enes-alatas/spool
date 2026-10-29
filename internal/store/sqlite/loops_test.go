package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// TestLoopWorkstationColumns pins the workstation config contract (ADR-0018):
// runtime/image/limits round-trip through Create, and Update cannot touch
// them — immutability is enforced by omission from the UPDATE statement.
func TestLoopWorkstationColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "walled", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeDocker, Image: "spool-workstation", MemMB: 4096, CPUs: 2,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}

	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != store.RuntimeDocker || got.Image != "spool-workstation" ||
		got.MemMB != 4096 || got.CPUs != 2 {
		t.Fatalf("workstation config did not round-trip: %+v", got)
	}

	mission := "updated"
	after, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 1})
	if err != nil {
		t.Fatal(err)
	}
	if after.Mission != "updated" {
		t.Fatalf("mission not updated: %+v", after)
	}
	if after.Runtime != store.RuntimeDocker || after.Image != "spool-workstation" ||
		after.MemMB != 4096 || after.CPUs != 2 {
		t.Fatalf("workstation config must be immutable through an edit: %+v", after)
	}
}

// TestLoopRehome: a rehome makes a bare worktree loop a docker one whose
// workspace is the workstation's home, and leaves the host workspace behind
// (#624). It is the one write that changes a loop's runtime.
func TestLoopRehome(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "mover", Mission: "m", Status: store.StatusActive, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, WorkspaceMode: store.WorkspaceWorktree, WorkspacePath: "/srv/fixture/wt/mover",
		RepoPath: "/srv/fixture/repo", WorktreePath: "/srv/fixture/wt/mover", Branch: "loop/mover",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Rehome(ctx, "l1", "/home/loop", 4096, 2, now+1); err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != store.RuntimeDocker || got.WorkspaceMode != store.WorkspaceNone || got.WorkspacePath != "/home/loop" ||
		got.RepoPath != "" || got.WorktreePath != "" || got.Branch != "" || got.MemMB != 4096 || got.CPUs != 2 {
		t.Fatalf("rehomed loop = %+v", got)
	}
	if err := db.Loops().Rehome(ctx, "loop_gone", "/home/loop", 4096, 2, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rehoming a gone loop: err = %v, want ErrNotFound", err)
	}
}

// TestLoopRuntimeChecked pins the CHECK constraint: only bare|docker are
// storable runtimes, so a caller bug cannot persist an unknown kind.
func TestLoopRuntimeChecked(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l2", Name: "bogus", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: "microvm", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(context.Background(), loop); err == nil {
		t.Fatal("creating a loop with an unknown runtime must fail")
	}
}

// TestLoopHubMCPToken pins the send-token contract (ADR-0026): Create mints a
// token when the caller leaves it empty, the token resolves its loop, and an
// unknown or empty bearer is ErrNotFound.
func TestLoopHubMCPToken(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	for _, name := range []string{"terra", "iris"} {
		loopRecord := &store.Loop{
			ID: "l_" + name, Name: name, Mission: "m", Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
			Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Loops().Create(ctx, loopRecord); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if loopRecord.HubMCPToken == "" {
			t.Fatalf("create %s left HubMCPToken empty", name)
		}
	}

	terra, err := db.Loops().GetByName(ctx, "terra")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().GetByHubMCPToken(ctx, terra.HubMCPToken)
	if err != nil || got.ID != terra.ID {
		t.Fatalf("GetByHubMCPToken = %v, %v; want terra", got, err)
	}
	if _, err := db.Loops().GetByHubMCPToken(ctx, "nope"); err != store.ErrNotFound {
		t.Fatalf("unknown token: err = %v, want ErrNotFound", err)
	}
	if _, err := db.Loops().GetByHubMCPToken(ctx, ""); err != store.ErrNotFound {
		t.Fatalf("empty token: err = %v, want ErrNotFound", err)
	}

	// a rotated token revokes the old one at once, and the new one is read
	// back sealed as the first was
	rotated, err := db.Loops().RotateHubMCPToken(ctx, terra.ID)
	if err != nil || rotated == "" || rotated == terra.HubMCPToken {
		t.Fatalf("RotateHubMCPToken = %q, %v; want a new token", rotated, err)
	}
	if _, err := db.Loops().GetByHubMCPToken(ctx, terra.HubMCPToken); err != store.ErrNotFound {
		t.Errorf("the rotated-out token: err = %v, want ErrNotFound", err)
	}
	if got, err := db.Loops().GetByHubMCPToken(ctx, rotated); err != nil || got.ID != terra.ID || got.HubMCPToken != rotated {
		t.Errorf("the rotated token resolves to %+v, %v; want terra holding it", got, err)
	}
	if _, err := db.Loops().RotateHubMCPToken(ctx, "l_gone"); err != store.ErrNotFound {
		t.Errorf("rotating a missing loop's token: err = %v, want ErrNotFound", err)
	}
}

// TestMigrateWithoutAllowlistedSender: a database holding loops and nobody
// allowlisted must still open. Migration 0014 backfills the owner from
// tg_senders, and a subquery with no rows would write NULL into a NOT NULL
// column — failing the migration, which is the difference between "no owner
// configured" and a server that cannot start (#73).
func TestMigrateWithoutAllowlistedSender(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "aster", Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone,
		Runtime: store.RuntimeBare, Pacing: store.PacingFixed, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// a sender who is known but not allowed must not be adopted either
	if err := db.TGSenders().Create(ctx, &store.TGSender{
		TGUserID: 7, Username: "pending", Status: store.SenderPending, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// rewind to the schema before 0014 and replay it against the populated
	// database — an upgrade, which is the only path that runs the backfill
	// with rows to back-fill
	for _, stmt := range []string{
		`ALTER TABLE loops DROP COLUMN owner_tg_user_id`,
		`ALTER TABLE loops DROP COLUMN owner_dm_chat_id`,
		`DELETE FROM schema_migrations WHERE version = '0014_loop_owner.sql'`,
	} {
		if _, err := db.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewinding the schema: %v", err)
		}
	}
	db.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopening a database with loops and no allowlisted sender: %v", err)
	}
	defer reopened.Close()
	loopRecord, err := reopened.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if loopRecord.OwnerTGUserID != 0 || loopRecord.OwnerDMChatID != 0 {
		t.Fatalf("owner = %d, chat = %d; want an ownerless loop", loopRecord.OwnerTGUserID, loopRecord.OwnerDMChatID)
	}
}

// TestLoopRotationState pins the rotation columns (#66): a loop starts with
// no rotation pending, no reason and no note, SetRotation round-trips all
// three, and Update — which the API calls with whatever a settings form
// sent — cannot silently erase a rotation in progress.
func TestLoopRotationState(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "shedder", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RotatePending || got.RotateReason != "" || got.HandoffNote != "" {
		t.Fatalf("a new loop starts mid-rotation: %+v", got)
	}

	if err := db.Loops().SetRotation(ctx, "l1", true, store.RotationReasonMission, "pick up the release"); err != nil {
		t.Fatal(err)
	}
	if got, err = db.Loops().Get(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if !got.RotatePending || got.RotateReason != store.RotationReasonMission || got.HandoffNote != "pick up the release" {
		t.Fatalf("rotation state did not round-trip: %+v", got)
	}

	mission := "edited elsewhere"
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 1}); err != nil {
		t.Fatal(err)
	}
	if !got.RotatePending || got.RotateReason != store.RotationReasonMission || got.HandoffNote != "pick up the release" {
		t.Fatalf("an unrelated edit dropped the rotation in progress: %+v", got)
	}
}

// TestLoopPromptHash pins the column that says which system prompt the
// current session is really running (#162). It starts empty — an unknown
// hash must read as "no difference", never as one — round-trips through its
// own setter, and survives an unrelated edit for the same reason the
// rotation columns do: a settings form carries the row as it was read, which
// may already be a wake behind the actor.
func TestLoopPromptHash(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "prompted", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PromptHash != "" {
		t.Fatalf("a loop with no session claims a prompt: %q", got.PromptHash)
	}

	if err := db.Loops().SetPromptHash(ctx, "l1", "abc123"); err != nil {
		t.Fatal(err)
	}
	mission := "edited elsewhere"
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 1}); err != nil {
		t.Fatal(err)
	}
	if got.PromptHash != "abc123" {
		t.Fatalf("prompt hash = %q, want it to round-trip and survive the edit", got.PromptHash)
	}
}

// A refusal belongs to the model it refused: other edits keep it, an edit
// of the model clears it in the same statement, and a refusal of a model
// already replaced is never written (#289).
func TestLoopModelRefusal(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "refused", Mission: "m", Model: "claude-nosuch-1", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().SetModelRefusal(ctx, "l1", "claude-nosuch-1", "no such model", now+1); err != nil {
		t.Fatal(err)
	}
	mission := "edited"
	got, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelRefusal != "no such model" {
		t.Fatalf("refusal = %q after a mission edit, want it kept", got.ModelRefusal)
	}
	model := "haiku"
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{Model: &model, UpdatedAt: now + 3}); err != nil {
		t.Fatal(err)
	}
	if got.ModelRefusal != "" || got.Model != "haiku" {
		t.Fatalf("after a model edit: model %q, refusal %q; want haiku and none", got.Model, got.ModelRefusal)
	}

	// The refused turn's result can reach the actor after the edit landed.
	if err := db.Loops().SetModelRefusal(ctx, "l1", "claude-nosuch-1", "no such model", now+4); err != nil {
		t.Fatal(err)
	}
	if got, err = db.Loops().Get(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if got.ModelRefusal != "" {
		t.Fatalf("refusal = %q written onto the replacing model, want none", got.ModelRefusal)
	}
}

// A doing is cut to MaxDoing characters here, after the redact decorator has
// seen it whole, and the same doing again keeps the time it was first set
// (#751).
func TestLoopDoing(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	loop := &store.Loop{
		ID: "l1", Name: "doer", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	set := func(doing string, at int64) bool {
		t.Helper()
		changed, err := db.Loops().SetDoing(ctx, "l1", doing, at)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	read := func() *store.Loop {
		t.Helper()
		got, err := db.Loops().Get(ctx, "l1")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if !set("implementing #123", 10) {
		t.Fatal("a new doing reported unchanged")
	}
	if set("implementing #123", 20) {
		t.Fatal("the same doing again reported changed")
	}
	if got := read(); got.Doing != "implementing #123" || got.DoingAt != 10 {
		t.Fatalf("after the same doing again: %q at %d, want it kept at 10", got.Doing, got.DoingAt)
	}

	long := strings.Repeat("é", store.MaxDoing-1) + " tail"
	if !set(long, 30) {
		t.Fatal("a long doing reported unchanged")
	}
	cut := strings.Repeat("é", store.MaxDoing-1)
	if got := read(); got.Doing != cut || got.DoingAt != 30 {
		t.Fatalf("a long doing stored as %q at %d, want %q at 30", got.Doing, got.DoingAt, cut)
	}
	if set(long, 40) {
		t.Fatal("the same long doing again reported changed")
	}

	if !set("", 50) || read().Doing != "" {
		t.Fatal("an empty doing did not clear it")
	}
}

// TestTelegramColumnsSurviveAConcurrentWriter pins why the Telegram columns
// have their own setters instead of going through Update. Two writers touch a
// loop row at once — the hub assigning an owner, the poller binding a group —
// and neither holds the other's value. Written as the interleaving that
// actually happens: both read, then both write (#161).
func TestTelegramColumnsSurviveAConcurrentWriter(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "alpha", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Both writers read the row before either has written: this is the state
	// each one is holding when it decides what to do.
	if _, err := db.Loops().Get(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Loops().Get(ctx, "l1"); err != nil {
		t.Fatal(err)
	}

	if err := db.Loops().SetOwner(ctx, "l1", 5454, 0, now+1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-100123", store.FleetChannel, now+2); err != nil {
		t.Fatal(err)
	}

	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerTGUserID != 5454 {
		t.Errorf("owner = %d, want it to survive the group bind", got.OwnerTGUserID)
	}
	if got.TGGroupChatID != -100123 {
		t.Errorf("group chat = %d, want it to survive the owner write", got.TGGroupChatID)
	}

	// And the operator's edit, decided from the row as it was before either
	// write: it names a mission and nothing else, so it must leave both
	// columns where the poller put them. This used to be a whole-row write
	// that reverted them (#164) — the assertion is inverted here, and the
	// shape of Edit is what makes the revert unspellable.
	mission := "edited while the bots were busy"
	survived, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 3})
	if err != nil {
		t.Fatal(err)
	}
	if survived.OwnerTGUserID != 5454 || survived.TGGroupChatID != -100123 {
		t.Fatalf("an edit that named only the mission reverted a concurrent write: %+v", survived)
	}
	if survived.Mission != mission {
		t.Fatalf("mission = %q, want the edit to have landed", survived.Mission)
	}

	// Capturing the owner's DM chat must not disturb the binding either.
	if err := db.Loops().SetOwner(ctx, "l1", 5454, 0, now+4); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Rooms().Bind(ctx, "l1", store.SurfaceTelegram, "-100123", store.FleetChannel, now+4); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().SetOwnerDMChat(ctx, "l1", 777, now+5); err != nil {
		t.Fatal(err)
	}
	// Pausing a loop and switching its workstation off are the other two
	// single-field writers, and they run from the hub and the actor goroutine
	// while the poller is writing the columns above.
	if err := db.Loops().SetStatus(ctx, "l1", store.StatusPaused, now+6); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().SetWorkstationOff(ctx, "l1", true, now+7); err != nil {
		t.Fatal(err)
	}
	final, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if final.OwnerDMChatID != 777 || final.OwnerTGUserID != 5454 || final.TGGroupChatID != -100123 {
		t.Fatalf("a later single-field write disturbed its neighbours: %+v", final)
	}
	if final.Status != store.StatusPaused || !final.WorkstationOff {
		t.Fatalf("status/power did not land: %+v", final)
	}
}

// TestLoopFleetChannelMembership pins that a loop starts in the fleet channel
// and that leaving it is the operator's edit alone: an edit that does not
// name it leaves the loop where it was, whichever side that is (ADR-0032).
func TestLoopFleetChannelMembership(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "private", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OutsideFleetChannel {
		t.Fatal("a new loop starts outside the fleet channel; it should start in it")
	}

	outside, mission := true, "m2"
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{OutsideFleetChannel: &outside, UpdatedAt: now + 1}); err != nil {
		t.Fatal(err)
	}
	if !got.OutsideFleetChannel {
		t.Fatal("the edit taking the loop out of the fleet channel did not land")
	}
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 2}); err != nil {
		t.Fatal(err)
	}
	if !got.OutsideFleetChannel {
		t.Fatal("an edit that did not name the fleet channel put the loop back in it")
	}

	inside := false
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{OutsideFleetChannel: &inside, UpdatedAt: now + 3}); err != nil {
		t.Fatal(err)
	}
	if got.OutsideFleetChannel {
		t.Fatal("the edit putting the loop back in the fleet channel did not land")
	}
}

// A write to a loop that is gone says so, rather than succeeding at nothing
// or failing as if the store broke: the API answers it 404 (#180).
func TestWritesToAGoneLoopAreNotFound(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	mission := "Keep the ledger."
	if _, err := db.Loops().Edit(ctx, "loop_gone", store.LoopEdit{Mission: &mission, UpdatedAt: now}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Edit: err = %v, want ErrNotFound", err)
	}
	if err := db.Loops().SetOwner(ctx, "loop_gone", 42, 0, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetOwner: err = %v, want ErrNotFound", err)
	}
	if err := db.Connections().Create(ctx, &store.Connection{Name: "fixture", Kind: store.ConnectionEnvVar,
		Config: store.ConnectionConfig{Env: "FIXTURE_NAME"}, Secret: "fixture-value", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.Connections().Attach(ctx, "fixture", "loop_gone", now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Connections.Attach: err = %v, want ErrNotFound", err)
	}
}

// A cloud loop is stored as any other (ADR-0056), and the runtime column
// still refuses a kind no Spool runs: migration 0058 widened its CHECK in
// place rather than dropping it.
func TestLoopRuntimeCloud(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "cirrus", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeCloud, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatalf("store a cloud loop: %v", err)
	}
	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != store.RuntimeCloud {
		t.Fatalf("runtime = %q, want %q", got.Runtime, store.RuntimeCloud)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE loops SET runtime = 'vm' WHERE id = 'l1'`); err == nil {
		t.Fatal("the runtime column took a kind no Spool runs")
	}
	var check string
	if err := db.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
}

// A cloud loop's repos and environment round-trip, and an edit replaces
// them whole (ADR-0061).
func TestLoopCloudSetup(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "c1", Name: "cloudy", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeCloud,
		CloudRepo: "owner/first", CloudBranch: "main", CloudRepos: []string{"owner/second"}, CloudEnvironmentID: "env_01a",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := store.CloudSetupOf(loop); !slices.Equal(got.CloudRepos, want.Repos) || got.CloudRepo != want.Repo ||
		got.CloudBranch != want.Branch || got.CloudEnvironmentID != want.EnvironmentID {
		t.Fatalf("the cloud setup didn't round-trip: %+v", store.CloudSetupOf(got))
	}
	edited, err := db.Loops().Edit(ctx, "c1", store.LoopEdit{
		Cloud: &store.CloudSetup{Repo: "owner/third", Branch: "dev", EnvironmentID: "env_02b"}, UpdatedAt: now + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.CloudRepo != "owner/third" || edited.CloudBranch != "dev" || edited.CloudRepos != nil || edited.CloudEnvironmentID != "env_02b" {
		t.Fatalf("an edit must replace the setup whole: %+v", store.CloudSetupOf(edited))
	}
}
