package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func openSlackTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx := context.Background()
	if err := database.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "slacker", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return database, ctx
}

// A Slack app is attached, bound and owned, then detached (#230). The
// identity moves as one, the binding and the owner are the narrow writers'
// columns, and detaching drops the app, the binding and the DM channel the
// app's bot opened, but keeps the owner, so re-attaching in the same
// workspace does not ask for one again.
func TestLoopSlackIdentity(t *testing.T) {
	database, ctx := openSlackTestDB(t)
	loops := database.Loops()

	identity := store.SlackIdentity{
		AppToken: "xapp-synthetic", BotToken: "xoxb-synthetic",
		BotUserID: "U0BOT", BotName: "slacker", TeamID: "T0TEAM", TeamName: "Acme",
	}
	attached, err := loops.Edit(ctx, "l1", store.LoopEdit{Slack: &identity, UpdatedAt: 2})
	if err != nil {
		t.Fatal(err)
	}
	if attached.SlackAppToken != identity.AppToken || attached.SlackBotToken != identity.BotToken ||
		attached.SlackBotUserID != "U0BOT" || attached.SlackBotName != "slacker" ||
		attached.SlackTeamID != "T0TEAM" || attached.SlackTeamName != "Acme" {
		t.Fatalf("identity did not round-trip: %+v", attached)
	}

	if err := loops.SetSlackBinding(ctx, "l1", "C0FLEET", 3, 3); err != nil {
		t.Fatal(err)
	}
	if err := loops.SetSlackOwner(ctx, "l1", "U0OWNER", "", 4); err != nil {
		t.Fatal(err)
	}
	if err := loops.SetSlackOwnerDM(ctx, "l1", "D0OWNER", 5); err != nil {
		t.Fatal(err)
	}

	// An edit that does not name the app leaves all of it alone.
	mission := "still here"
	edited, err := loops.Edit(ctx, "l1", store.LoopEdit{Mission: &mission, UpdatedAt: 6})
	if err != nil {
		t.Fatal(err)
	}
	if edited.SlackBotToken == "" || edited.SlackChannelID != "C0FLEET" || edited.SlackChannelBoundAt != 3 ||
		edited.OwnerSlackUserID != "U0OWNER" || edited.OwnerSlackDMChannel != "D0OWNER" {
		t.Fatalf("an unrelated edit touched the Slack columns: %+v", edited)
	}

	detached, err := loops.Edit(ctx, "l1", store.LoopEdit{Slack: &store.SlackIdentity{}, ClearSlackBinding: true, UpdatedAt: 7})
	if err != nil {
		t.Fatal(err)
	}
	if detached.SlackAppToken != "" || detached.SlackBotToken != "" || detached.SlackBotUserID != "" ||
		detached.SlackTeamID != "" || detached.SlackChannelID != "" || detached.SlackChannelBoundAt != 0 {
		t.Fatalf("detach left part of the app behind: %+v", detached)
	}
	if detached.OwnerSlackUserID != "U0OWNER" {
		t.Fatalf("detach dropped the owner: %+v", detached)
	}
	if detached.OwnerSlackDMChannel != "" {
		t.Fatalf("detach kept the old bot's DM channel %q: it names a conversation the next app is not in",
			detached.OwnerSlackDMChannel)
	}

	if err := loops.SetSlackOwner(ctx, "gone", "U0OWNER", "", 8); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetSlackOwner on a gone loop = %v, want ErrNotFound", err)
	}
}

// The Slack allowlist behaves as tg_senders does: pending on first sight,
// one row per user, status changes by id, and an unknown id is not found.
func TestSlackSenders(t *testing.T) {
	database, ctx := openSlackTestDB(t)
	senders := database.SlackSenders()

	sender := &store.SlackSender{
		SlackUserID: "U0ALICE", TeamID: "T0TEAM", Username: "alice", Display: "Alice",
		Status: store.SenderPending, PairCode: "123456", FirstSeenVia: "dm:slacker", CreatedAt: 1, UpdatedAt: 1,
	}
	if err := senders.Create(ctx, sender); err != nil {
		t.Fatal(err)
	}
	if err := senders.Create(ctx, sender); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("second Create = %v, want ErrDuplicate", err)
	}
	if err := senders.SetStatus(ctx, "U0ALICE", store.SenderAllowed, 2); err != nil {
		t.Fatal(err)
	}
	got, err := senders.Get(ctx, "U0ALICE")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.SenderAllowed || got.TeamID != "T0TEAM" || got.PairCode != "123456" || got.UpdatedAt != 2 {
		t.Fatalf("sender = %+v", got)
	}
	if err := senders.SetStatus(ctx, "U0NOBODY", store.SenderAllowed, 3); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetStatus on an unknown sender = %v, want ErrNotFound", err)
	}

	listed, err := senders.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].SlackUserID != "U0ALICE" {
		t.Fatalf("List = %v, %v", listed, err)
	}
	if err := senders.Delete(ctx, "U0ALICE"); err != nil {
		t.Fatal(err)
	}
	if _, err := senders.Get(ctx, "U0ALICE"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
}
