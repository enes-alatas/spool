package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

func TestPresentAttachmentsByRuntime(t *testing.T) {
	rows := []*store.Attachment{
		{Kind: store.AttachmentImage, Name: "shot.png", Size: 2048, Width: 8, Height: 4, Path: "ab-shot.png"},
		{Kind: store.AttachmentFile, Name: "big.bin", Size: 30 << 20, NotKept: store.NotKeptTooLarge},
		{Kind: store.AttachmentFile, Name: "old.txt", Size: 3, Path: "cd-old.txt", RemovedAt: 1},
	}
	hostPath := func(rel string) string { return "/data/files/" + rel }

	shown, copies := PresentAttachments(&store.Loop{Runtime: store.RuntimeBare}, rows, hostPath)
	if len(copies) != 0 || shown[0].Path != "/data/files/ab-shot.png" {
		t.Errorf("bare: shown %+v, copies %+v — want the hub's copy and nothing to copy", shown[0], copies)
	}

	shown, copies = PresentAttachments(&store.Loop{Runtime: store.RuntimeDocker}, rows, hostPath)
	want := WorkstationFilesDir + "/ab-shot.png"
	if shown[0].Path != want || len(copies) != 1 || copies[0] != (FileCopy{From: "/data/files/ab-shot.png", To: want}) {
		t.Errorf("docker: shown %+v, copies %+v — want one copy into the workstation", shown[0], copies)
	}
	if shown[1].Path != "" || shown[1].NotKept != store.NotKeptTooLarge {
		t.Errorf("too large: %+v", shown[1])
	}
	if shown[2].Path != "" || shown[2].NotKept != NotKeptRemoved {
		t.Errorf("expired: %+v", shown[2])
	}
}

func TestAttachmentsComeBetweenTheHeaderAndTheWords(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	env := MessageEnvelope(now, Inbound{
		Origin: store.OriginTelegramDM, Author: "enes", Text: "see this", Conversation: store.ConversationOwnerDM,
		Ref: "ref:5",
		Attachments: []Attachment{
			{Kind: store.AttachmentImage, Name: "shot.png", Size: 245_760, Width: 1280, Height: 720, Path: "/w/shot.png"},
			{Kind: store.AttachmentFile, Name: "dump.bin", Size: 35_756_032, NotKept: store.NotKeptTooLarge},
			{Kind: store.AttachmentFile, Name: "old.log", Size: 12, NotKept: NotKeptRemoved},
		},
	})
	want := "[message from @enes via telegram dm · owner_dm · ref:5 · 2026-09-30 10:00 UTC]\n" +
		"[image: shot.png · 1280×720 · 240 KB · /w/shot.png]\n" +
		"[file not kept: dump.bin · 34.1 MB · over the 20 MB limit]\n" +
		"[file not kept: old.log · 12 B · no longer kept]\n" +
		"\nsee this"
	if env.Text != want {
		t.Errorf("envelope:\n%s\nwant:\n%s", env.Text, want)
	}

	// a photo with no caption is the header and the attachment, nothing after
	bare := MessageEnvelope(now, Inbound{Origin: store.OriginTelegramDM, Author: "enes",
		Attachments: []Attachment{{Kind: store.AttachmentFile, Name: "a.txt", Size: 12, Path: "/w/a.txt"}}})
	if !strings.HasSuffix(bare.Text, "]\n[file: a.txt · 12 B · /w/a.txt]") {
		t.Errorf("uncaptioned envelope ends %q", bare.Text)
	}
}

func TestFilesNotCopiedNote(t *testing.T) {
	if FilesNotCopiedNote(nil) != "" {
		t.Error("a note for no failures")
	}
	if note := FilesNotCopiedNote([]string{"/w/a", "/w/b"}); !strings.Contains(note, "/w/a, /w/b") {
		t.Errorf("note %q does not name the paths", note)
	}
}
