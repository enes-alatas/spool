package route

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/redact"
	"github.com/enes-alatas/spool/internal/store"
)

func (table *memAttachments) Get(_ context.Context, id int64) (*store.Attachment, error) {
	for _, row := range table.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return nil, store.ErrNotFound
}

func (table *memAttachments) RemoveUnsent(_ context.Context, id, removedAt int64) (*store.Attachment, error) {
	for _, row := range table.rows {
		if row.ID == id && row.MessageID == 0 && row.RemovedAt == 0 && row.Path != "" {
			row.RemovedAt = removedAt
			return row, nil
		}
	}
	return nil, store.ErrNotFound
}

var (
	cloudLoopA = &store.Loop{ID: "loop-a", Name: "ash", Runtime: store.RuntimeCloud}
	cloudLoopB = &store.Loop{ID: "loop-b", Name: "birch", Runtime: store.RuntimeCloud}
	callA      = CloudCall{Session: "cse_01SyntheticA", ToolUseID: "toolu_01SyntheticA"}
)

func cloudSend(from *store.Loop, call CloudCall) SendRequest {
	return SendRequest{From: from, Destination: store.ConversationOwnerDM, Text: "the file", Attach: "report.txt", Cloud: call}
}

// A cloud loop's upload is claimed by the send it was posted for, once,
// and only by that loop's session's call: another loop, another session
// of the same loop or another call naming it finds no upload (ADR-0073).
func TestACloudUploadIsClaimedOnceByItsOwnCall(t *testing.T) {
	router, table, files := testRouter(t)
	ctx := context.Background()
	if serr, err := router.KeepCloudUpload(ctx, cloudLoopA, callA, "report.txt", []byte("the report")); serr != nil || err != nil {
		t.Fatalf("keep: %v, %v", serr, err)
	}
	for name, req := range map[string]SendRequest{
		"another loop":    cloudSend(cloudLoopB, callA),
		"another session": cloudSend(cloudLoopA, CloudCall{Session: "cse_01SyntheticOther", ToolUseID: callA.ToolUseID}),
		"another call":    cloudSend(cloudLoopA, CloudCall{Session: callA.Session, ToolUseID: "toolu_01SyntheticOther"}),
		"no call":         cloudSend(cloudLoopA, CloudCall{}),
	} {
		kept, serr, err := router.keepSent(ctx, req)
		if err != nil || kept != nil || serr == nil || serr.Code != ErrAttachmentNotFound || !strings.Contains(serr.Detail, "didn't reach the hub") {
			t.Errorf("%s: kept %+v, %v, %v; want no upload waiting", name, kept, serr, err)
		}
	}
	kept, serr, err := router.keepSent(ctx, cloudSend(cloudLoopA, callA))
	if err != nil || serr != nil || kept == nil || kept.ID == 0 || kept.Name != "report.txt" {
		t.Fatalf("its own call: kept %+v, %v, %v; want the upload", kept, serr, err)
	}
	if body, err := os.ReadFile(files.Path(kept.Path)); err != nil || string(body) != "the report" {
		t.Errorf("the kept file holds %q, %v", body, err)
	}
	if _, serr, _ := router.keepSent(ctx, cloudSend(cloudLoopA, callA)); serr == nil {
		t.Error("a second send claimed the same upload")
	}
	if len(table.rows) != 1 || table.rows[0].MessageID != 0 {
		t.Errorf("rows %+v, want the one upload, unsent until its message claims it", table.rows)
	}
}

// An upload its send never came for goes at the loop's Stop, its row
// marked and its file deleted; another loop's stays.
func TestCloudUploadsNoSendClaimedGoAtStop(t *testing.T) {
	router, table, files := testRouter(t)
	ctx := context.Background()
	for _, loopRecord := range []*store.Loop{cloudLoopA, cloudLoopB} {
		if serr, err := router.KeepCloudUpload(ctx, loopRecord, callA, "report.txt", []byte("the report")); serr != nil || err != nil {
			t.Fatalf("keep: %v, %v", serr, err)
		}
	}
	router.DropCloudUploads(ctx, cloudLoopA.ID)
	if table.rows[0].RemovedAt == 0 || table.rows[1].RemovedAt != 0 {
		t.Errorf("rows %+v, want loop A's removed and B's kept", table.rows)
	}
	if _, err := os.Stat(files.Path(table.rows[0].Path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("loop A's file is still kept: %v", err)
	}
	if _, serr, _ := router.keepSent(ctx, cloudSend(cloudLoopA, callA)); serr == nil {
		t.Error("a dropped upload was claimed")
	}
	if kept, serr, _ := router.keepSent(ctx, cloudSend(cloudLoopB, callA)); serr != nil || kept == nil {
		t.Errorf("loop B's upload: %v", serr)
	}
}

// The same call posting again, as curl does after a lost answer, replaces
// what it posted; at most CloudUploadsWaiting of one loop's wait at once.
func TestCloudUploadsAreBoundedPerLoop(t *testing.T) {
	router, table, _ := testRouter(t)
	ctx := context.Background()
	for range 2 {
		if serr, err := router.KeepCloudUpload(ctx, cloudLoopA, callA, "report.txt", []byte("the report")); serr != nil || err != nil {
			t.Fatalf("keep: %v, %v", serr, err)
		}
	}
	if table.rows[0].RemovedAt == 0 || table.rows[1].RemovedAt != 0 {
		t.Errorf("rows %+v, want the first post replaced by the second", table.rows)
	}
	for i := 1; i < CloudUploadsWaiting; i++ {
		call := CloudCall{Session: callA.Session, ToolUseID: callA.ToolUseID + strings.Repeat("x", i)}
		if serr, err := router.KeepCloudUpload(ctx, cloudLoopA, call, "report.txt", []byte("the report")); serr != nil || err != nil {
			t.Fatalf("keep %d: %v, %v", i, serr, err)
		}
	}
	over := CloudCall{Session: callA.Session, ToolUseID: "toolu_01SyntheticOver"}
	if serr, _ := router.KeepCloudUpload(ctx, cloudLoopA, over, "report.txt", []byte("the report")); serr == nil || !strings.Contains(serr.Detail, "already waiting") {
		t.Errorf("a fifth waiting upload: %v, want refused", serr)
	}
	if serr, _ := router.KeepCloudUpload(ctx, cloudLoopB, over, "report.txt", []byte("the report")); serr != nil {
		t.Errorf("another loop's upload was refused for this one's: %v", serr)
	}
}

// What keepSent checks of a workstation's file is checked of a cloud
// upload: the name is redacted before anything is derived from it, a
// file over the limit is refused naming it, and so is one holding a
// secret the hub knows. Nothing refused is kept.
func TestACloudUploadIsCheckedAsAnyAttachment(t *testing.T) {
	router, table, _ := testRouter(t)
	ctx := context.Background()
	secret := "sk-ant-oat01-" + strings.Repeat("fixture", 6) // synthetic
	redactor := redact.New(redact.Fixed{{Name: "claude_token", Value: secret}}, time.Hour)
	if err := redactor.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	router.redactName = redactor.Text

	serr, _ := router.KeepCloudUpload(ctx, cloudLoopA, callA, "env.txt", []byte("TOKEN="+secret))
	if serr == nil || serr.Code != ErrSecretInAttachment {
		t.Errorf("a file with a secret: %v, want refused", serr)
	}
	serr, _ = router.KeepCloudUpload(ctx, cloudLoopA, callA, "big.bin", make([]byte, attach.MaxSize+1))
	if serr == nil || serr.Code != ErrAttachmentTooLarge || !strings.Contains(serr.Detail, "big.bin is over the 20 MB limit") {
		t.Errorf("a file over the limit: %v, want refused naming it", serr)
	}
	if len(table.rows) != 0 {
		t.Errorf("refused uploads were kept: %+v", table.rows)
	}
	if serr, err := router.KeepCloudUpload(ctx, cloudLoopA, callA, secret+".txt", []byte("fine")); serr != nil || err != nil {
		t.Fatalf("keep: %v, %v", serr, err)
	}
	if len(table.rows) != 1 || strings.Contains(table.rows[0].Name, secret) || strings.Contains(table.rows[0].Path, secret) {
		t.Errorf("the name was kept unredacted: %+v", table.rows)
	}
}
