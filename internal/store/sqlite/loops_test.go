package sqlite

import (
	"context"
	"path/filepath"
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
		l := &store.Loop{
			ID: "l_" + name, Name: name, Mission: "m", Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
			Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Loops().Create(ctx, l); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if l.HubMCPToken == "" {
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
	l, err := reopened.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if l.OwnerTGUserID != 0 || l.OwnerDMChatID != 0 {
		t.Fatalf("owner = %d, chat = %d; want an ownerless loop", l.OwnerTGUserID, l.OwnerDMChatID)
	}
}

// TestLoopRotationState pins the rotation columns (#66): a loop starts with
// no rotation pending and no note, SetRotation round-trips both, and Update —
// which the API calls with whatever a settings form sent — cannot silently
// erase a rotation in progress.
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
	if got.RotatePending || got.HandoffNote != "" {
		t.Fatalf("a new loop starts mid-rotation: %+v", got)
	}

	if err := db.Loops().SetRotation(ctx, "l1", true, "pick up the release"); err != nil {
		t.Fatal(err)
	}
	if got, err = db.Loops().Get(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if !got.RotatePending || got.HandoffNote != "pick up the release" {
		t.Fatalf("rotation state did not round-trip: %+v", got)
	}

	mission := "edited elsewhere"
	if got, err = db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: now + 1}); err != nil {
		t.Fatal(err)
	}
	if !got.RotatePending || got.HandoffNote != "pick up the release" {
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
	if err := db.Loops().SetGroupBinding(ctx, "l1", -100123, now+2, now+2); err != nil {
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
	if err := db.Loops().SetGroupBinding(ctx, "l1", -100123, now+4, now+4); err != nil {
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
