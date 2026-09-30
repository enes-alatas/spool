package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// TestExportReadsASealedHubAndMasksItsFiles exports from a store sealed
// under its hub key, as every hub's is, whose one recent turn quotes the
// key and the operator token: the case comes out, and neither value does.
func TestExportReadsASealedHubAndMasksItsFiles(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "spool.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	keyHex := hex.EncodeToString(db.HubKey())
	// synthetic: made here, never a real token
	operatorToken := "synthetic-" + strings.Repeat("0a", 20)
	if err := os.WriteFile(filepath.Join(dataDir, operator.TokenFile), []byte(operatorToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UnixMilli()
	if err := db.Loops().Create(ctx, &store.Loop{ID: "l1", Name: "terra", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Runtime: store.RuntimeBare, Pacing: store.PacingSelf,
		MinWakeSec: 300, MaxWakeSec: 14400, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]string{"trigger": "message", "conversation": "group",
		"text": "[message from @quinn (loop) · channel:fleet · ref:5]\n\nkey " + keyHex + " token " + operatorToken})
	day := int64(24 * time.Hour / time.Millisecond)
	for _, event := range []*store.Event{
		// a turn from 40 days ago, older than -since
		{TurnID: "old", TS: now - 40*day, Type: "envelope", Payload: string(envelope)},
		{TurnID: "old", TS: now - 40*day, Type: "result", Payload: `{"type":"result","result":"ok\n[next-wake: 1h]"}`},
		{TurnID: "recent", TS: now, Type: "envelope", Payload: string(envelope)},
		{TurnID: "recent", TS: now, Type: "result", Payload: `{"type":"result","result":"ok\n[next-wake: 1h]"}`},
	} {
		event.LoopID, event.SessionID = "l1", "s1"
		if _, err := db.Events().Insert(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	before := names(t, dataDir)
	out := filepath.Join(t.TempDir(), "cases")
	if err := export([]string{"-db", dbPath, "-out", out, "-rules", "../../scripts/secret-rules.awk", "-since", "168h"}); err != nil {
		t.Fatalf("export of a sealed store: %v", err)
	}
	cases, err := filepath.Glob(filepath.Join(out, "*.json"))
	if err != nil || len(cases) != 1 || !strings.Contains(cases[0], "recent") {
		t.Fatalf("cases = %v, %v; want the recent turn alone", cases, err)
	}
	written := []string{cases[0]}
	for _, path := range written {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), keyHex) || strings.Contains(string(raw), operatorToken) {
			t.Errorf("%s holds the hub key or the operator token", filepath.Base(path))
		}
	}
	if raw, _ := os.ReadFile(cases[0]); !strings.Contains(string(raw), "redacted") {
		t.Errorf("the case shows no redaction: %s", raw)
	}
	// The export writes nothing beside the live store. SQLite gives any
	// reader of a WAL database its -shm and -wal files, which a running
	// hub's store always has, so those two are not the export's.
	if after := names(t, dataDir); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("data dir went from %v to %v", before, after)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if name := entry.Name(); !strings.HasSuffix(name, "-shm") && !strings.HasSuffix(name, "-wal") {
			out = append(out, name)
		}
	}
	return out
}
