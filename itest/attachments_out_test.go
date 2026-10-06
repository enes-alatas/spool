//go:build integration

package itest

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A loop sends a file with send_message's attach (#123). It may send only
// a file it owns: on a bare loop, one inside its workspace.

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// keptFiles lists the hub's files directory.
func keptFiles(t *testing.T, s *server) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "files"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// waitMedia waits for a file sent to chatID by the given Bot API method.
func (tg *fakeTelegram) waitMedia(t *testing.T, chatID int64, method string) sentMessage {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range tg.sentTo(chatID) {
			if m.Method == method {
				return m
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no %s to chat %d; sent %+v", method, chatID, tg.sentTo(chatID))
	return sentMessage{}
}

func TestALoopSendsItsOwnerAPhotoWithItsWordsAsTheCaption(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6161, First: "Operator", Username: "operator"}
	ws := t.TempDir()
	shot := pngOf(t, 40, 20)
	writeFile(t, filepath.Join(ws, "shots", "home.png"), shot)
	report := []byte("line one\nline two\n")
	writeFile(t, filepath.Join(ws, "report.txt"), report)
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws, "workspace_mode": "dir"})

	// the owner writes first: a bot cannot open a private chat
	tg.dm("alpha", operator, "send me the screenshot")
	envelopeWith(t, srv, "alpha", "send me the screenshot")
	sess := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))

	res := callSend(t, sess, map[string]any{"destination": "owner_dm", "text": "the home page", "attach": "shots/home.png"})
	if res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	photo := tg.waitMedia(t, operator.ID, "sendPhoto")
	if photo.Token != "alpha" || photo.Text != "the home page" || photo.FileName != "home.png" || !bytes.Equal(photo.File, shot) {
		t.Errorf("sent %s %q by %s with %d bytes, want alpha's photo of home.png captioned", photo.FileName, photo.Text, photo.Token, len(photo.File))
	}

	// words too long for a caption go first, and the file follows them
	long := "the report: " + strings.Repeat("all green. ", 120)
	res = callSend(t, sess, map[string]any{"destination": "owner_dm", "text": long, "attach": filepath.Join(ws, "report.txt")})
	if res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	document := tg.waitMedia(t, operator.ID, "sendDocument")
	words := tg.waitSent(t, operator.ID, "the report: ")
	if document.Text != "" || !bytes.Equal(document.File, report) || document.MessageID < words.MessageID {
		t.Errorf("document %+v did not follow the words %+v uncaptioned", document, words)
	}
}

// A file sent to the group reaches the loops it names as a path they can
// read, as a human's does.
func TestAFileSentToTheGroupReachesTheLoopsItNames(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	ws := t.TempDir()
	log := []byte("panic: at the disco\n")
	writeFile(t, filepath.Join(ws, "crash.log"), log)
	s.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	s.createLoop("briar", nil)
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	res := callSend(t, sess, map[string]any{"destination": "group", "text": "@briar the crash", "attach": "crash.log"})
	if res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	envelope := envelopeWith(t, s, "briar", "the crash")
	if !strings.Contains(envelope, "[file: crash.log · 20 B · ") {
		t.Fatalf("briar was not shown the file:\n%s", envelope)
	}
	if got, err := os.ReadFile(attachmentPath(t, envelope)); err != nil || !bytes.Equal(got, log) {
		t.Errorf("briar cannot read the file it was shown: %v", err)
	}
}

// Every way a bare loop could reach past its workspace is refused, with a
// code the loop can act on, and none of them stores, keeps or sends a thing.
func TestALoopCannotSendAFileItDoesNotOwn(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.txt")
	writeFile(t, outside, []byte("the operator's"))
	writeFile(t, filepath.Join(ws, "fine.txt"), []byte("mine"))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(dataDir, "spool.db")
	must(os.Symlink(db, filepath.Join(ws, "db-link")))
	must(os.Symlink(dataDir, filepath.Join(ws, "data-link")))
	must(os.Symlink(outside, filepath.Join(ws, "out-link")))
	must(os.Link(outside, filepath.Join(ws, "hard-link")))
	must(os.Mkdir(filepath.Join(ws, "folder"), 0o700))
	big, err := os.Create(filepath.Join(ws, "big.bin"))
	must(err)
	must(big.Truncate(20<<20 + 1))
	must(big.Close())

	s.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	const secret = "itest-SYNTHETIC-attach-secret-77"
	s.setLoopEnv("aster", "ATTACH_TOKEN", secret)
	writeFile(t, filepath.Join(ws, "notes.txt"), []byte("token: "+secret+"\n"))
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	for path, code := range map[string]string{
		db:                             "attachment_not_owned",
		"../" + filepath.Base(dataDir): "attachment_not_owned",
		filepath.Join(ws, "..", "x"):   "attachment_not_owned",
		outside:                        "attachment_not_owned",
		"db-link":                      "attachment_not_owned",
		"data-link/spool.db":           "attachment_not_owned",
		"out-link":                     "attachment_not_owned",
		"hard-link":                    "attachment_not_owned",
		"/etc/passwd":                  "attachment_not_owned",
		"missing.txt":                  "attachment_not_found",
		"folder":                       "attachment_not_found",
		"big.bin":                      "attachment_too_large",
		"notes.txt":                    "attachment_contains_secret",
	} {
		res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "refused " + path, "attach": path})
		if !res.IsError || !strings.Contains(resultText(res), code) {
			t.Errorf("%s: %s, want %s", path, resultText(res), code)
		}
	}
	if kept := keptFiles(t, s); len(kept) != 0 {
		t.Errorf("a refused file was kept: %v", kept)
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "refused ") {
			t.Errorf("a refused send was stored: %s", dump(m))
		}
	}

	// the same loop sends what it owns
	if res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "mine", "attach": "fine.txt"}); res.IsError {
		t.Fatalf("a file inside the workspace was refused: %s", resultText(res))
	}
	if kept := keptFiles(t, s); len(kept) != 1 || !strings.HasSuffix(kept[0], "-fine.txt") {
		t.Errorf("kept %v, want the one file sent", kept)
	}
}

// A workspace that holds the hub's data directory, as a home directory
// does, sends nothing: no file in it is the loop's alone.
func TestALoopWorkingAboveTheHubDataSendsNoFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".spool"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := startServer(t, filepath.Join(home, ".spool"))
	writeFile(t, filepath.Join(home, "notes.txt"), []byte("mine, or so it seems"))
	s.createLoop("aster", map[string]any{"workspace_path": home, "workspace_mode": "dir"})
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	for _, path := range []string{"notes.txt", ".spool/spool.db"} {
		res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "from home", "attach": path})
		wantSendError(t, res, "attachment_not_owned")
	}
}

// A send that failed is retried with its file: the row carries it, so the
// operator's retry sends the words and the file the loop sent together.
func TestARetriedSendCarriesItsFile(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6262, First: "Operator", Username: "operator"}
	ws := t.TempDir()
	shot := pngOf(t, 8, 8)
	writeFile(t, filepath.Join(ws, "shot.png"), shot)
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	tg.dm("alpha", operator, "hi")
	waitOwnerDMReady(t, srv, "alpha")
	sess := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))

	tg.failNextSends(-1)
	if res := callSend(t, sess, map[string]any{"destination": "owner_dm", "text": "the shot", "attach": "shot.png"}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	lost := srv.waitUndelivered(1, 60*time.Second)[0]

	tg.failNextSends(0)
	srv.mustJSON("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/retry", nil, nil)
	photo := tg.waitMedia(t, operator.ID, "sendPhoto")
	if photo.Text != "the shot" || !bytes.Equal(photo.File, shot) {
		t.Errorf("the retry sent %q with %d bytes, want the shot and its words", photo.Text, len(photo.File))
	}
	srv.waitUndelivered(0, 30*time.Second)
}
