package route

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// memAttachments is an attachment table in memory.
type memAttachments struct {
	store.AttachmentStore
	rows []*store.Attachment
}

func (table *memAttachments) Insert(_ context.Context, attachment *store.Attachment) error {
	attachment.ID = int64(len(table.rows) + 1)
	table.rows = append(table.rows, attachment)
	return nil
}

func (table *memAttachments) Expire(_ context.Context, cutoff, removedAt int64) ([]*store.Attachment, error) {
	var out []*store.Attachment
	for _, row := range table.rows {
		if row.RemovedAt == 0 && row.Path != "" && row.CreatedAt < cutoff {
			row.RemovedAt = removedAt
			out = append(out, row)
		}
	}
	return out, nil
}

type attachmentsOnly struct {
	store.Store
	table *memAttachments
}

func (fake attachmentsOnly) Attachments() store.AttachmentStore { return fake.table }

func testRouter(t *testing.T) (*Router, *memAttachments, *attach.Files) {
	t.Helper()
	table := &memAttachments{}
	router := New(attachmentsOnly{table: table}, nil, nil, nil)
	files, err := attach.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router.SetFiles(files, nil)
	return router, table, files
}

func fetching(body string) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
}

func TestEveryAttachmentIsRecordedKeptOrNot(t *testing.T) {
	router, _, files := testRouter(t)
	rows := router.keepAttachments(context.Background(), 7, []InboundAttachment{
		{Name: "notes.txt", Fetch: fetching("hello")},
		{Name: "huge.png", Size: attach.MaxSize + 1, Fetch: func(context.Context) (io.ReadCloser, error) {
			t.Error("an attachment declared over the limit was fetched")
			return nil, errors.New("unreachable")
		}},
		{Name: "gone.pdf", Fetch: func(context.Context) (io.ReadCloser, error) { return nil, errors.New("404") }},
	})
	if len(rows) != 3 {
		t.Fatalf("recorded %d, want all 3", len(rows))
	}
	if rows[0].Path == "" || rows[0].MessageID != 7 || rows[0].CreatedAt == 0 {
		t.Errorf("kept file row: %+v", rows[0])
	}
	if body, err := os.ReadFile(files.Path(rows[0].Path)); err != nil || string(body) != "hello" {
		t.Errorf("kept file reads %q, %v", body, err)
	}
	if rows[1].NotKept != store.NotKeptTooLarge || rows[1].Kind != store.AttachmentImage {
		t.Errorf("over-limit row: %+v", rows[1])
	}
	if rows[2].NotKept != store.NotKeptFailed || rows[2].Path != "" {
		t.Errorf("failed-fetch row: %+v", rows[2])
	}
}

func TestExpiryRemovesTheFileAndKeepsTheRow(t *testing.T) {
	router, table, files := testRouter(t)
	rows := router.keepAttachments(context.Background(), 1, []InboundAttachment{{Name: "a.txt", Fetch: fetching("a")}})
	rows[0].CreatedAt = 1 // kept long ago

	expired, err := router.ExpireAttachments(context.Background())
	if err != nil || expired != 1 {
		t.Fatalf("expired %d, %v; want 1", expired, err)
	}
	if _, err := os.Stat(files.Path(rows[0].Path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the expired file is still there: %v", err)
	}
	if len(table.rows) != 1 || table.rows[0].RemovedAt == 0 {
		t.Errorf("the row should stay, marked removed: %+v", table.rows)
	}
}

func TestAFileNameIsRedactedBeforeAnythingIsDerivedFromIt(t *testing.T) {
	router, _, _ := testRouter(t)
	router.redactName = func(name string) string { return strings.ReplaceAll(name, "tok:en", "<redacted:T>") }
	rows := router.keepAttachments(context.Background(), 1, []InboundAttachment{
		{Name: "a-tok:en.txt", Fetch: fetching("a")},
		{Name: "b-tok:en.bin", Size: attach.MaxSize + 1},
	})
	for _, row := range rows {
		for field, value := range map[string]string{"name": row.Name, "path": row.Path} {
			if strings.Contains(value, "tok") {
				t.Errorf("%s %q carries the secret", field, value)
			}
		}
	}
	if !strings.HasSuffix(rows[0].Path, "-a-_redacted_T_.txt") {
		t.Errorf("kept as %q", rows[0].Path)
	}
}

// workstation is a loop's files, or the error reading one gives.
type workstation map[string]any

func (files workstation) GetFile(_ context.Context, _ *store.Loop, path string, limit int64) ([]byte, error) {
	switch value := files[path].(type) {
	case string:
		if int64(len(value)) > limit {
			return nil, runtime.ErrFileTooLarge
		}
		return []byte(value), nil
	case error:
		return nil, value
	}
	return nil, runtime.ErrNoSuchFile
}

func TestASentFileIsRefusedForEachReasonTheLoopCanCorrect(t *testing.T) {
	router, _, files := testRouter(t)
	router.redactName = func(text string) string { return strings.ReplaceAll(text, "hunter2hunter2", "<redacted:PW>") }
	router.SetWorkstations(workstation{
		"shot.png":   "png bytes",
		"creds.txt":  "password=hunter2hunter2",
		"../x":       runtime.ErrNotOwned,
		"dir":        runtime.ErrNotAFile,
		"huge.bin":   runtime.ErrFileTooLarge,
		"broken.txt": errors.New("docker exec: container is not running"),
	})
	bare := &store.Loop{ID: "l1", WorkspacePath: "/w"}
	send := func(from *store.Loop, destination, path string) (*store.Attachment, *SendError, error) {
		return router.keepSent(context.Background(), SendRequest{From: from, Destination: destination, Attach: path})
	}

	for path, code := range map[string]string{
		"../x":      ErrAttachmentNotOwned,
		"nope.txt":  ErrAttachmentNotFound,
		"dir":       ErrAttachmentNotFound,
		"huge.bin":  ErrAttachmentTooLarge,
		"creds.txt": ErrSecretInAttachment,
	} {
		kept, serr, err := send(bare, store.ConversationOwnerDM, path)
		if err != nil || serr == nil || serr.Code != code || kept != nil {
			t.Errorf("%s: kept %v, refusal %v, err %v; want %s", path, kept, serr, err, code)
		}
	}
	if _, serr, err := send(bare, store.ConversationOwnerDM, "broken.txt"); serr != nil || err == nil {
		t.Errorf("a workstation that cannot be read is the hub's fault, not the loop's: %v, %v", serr, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(files.Path("x"))); len(entries) != 0 {
		t.Errorf("a refused file was kept: %v", entries)
	}

	kept, serr, err := send(bare, store.ConversationOwnerDM, "shot.png")
	if err != nil || serr != nil || kept == nil || kept.Name != "shot.png" {
		t.Fatalf("kept %+v, %v, %v", kept, serr, err)
	}
	if body, err := os.ReadFile(files.Path(kept.Path)); err != nil || string(body) != "png bytes" {
		t.Errorf("the kept copy reads %q, %v", body, err)
	}
}
