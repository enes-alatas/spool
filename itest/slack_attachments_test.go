//go:build integration

package itest

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Files cross Slack both ways (#123): what a person shares reaches the
// loops as a path they can read, and what a loop attaches is uploaded by
// its app after its words.

// A file shared in the channel is downloaded once, by the app whose ingest
// stored the message, and shown to every loop the message names. A file
// shared with no words still reaches the owner's loop, and one the event
// describes by its id alone is asked about first.
func TestSlackSharedFilesReachTheLoops(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": slackOperator}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}
	shot := pngOf(t, 64, 32)
	file := slack.addFile("F0SHOT", "layout.png", "image/png", shot)

	const ts = "1727600000.000500"
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushEvent(t, app, map[string]any{"type": "message", "subtype": "file_share", "channel_type": "channel",
			"channel": slackChannel, "user": slackOperator, "text": "<@U0TERRA> <@U0MILO> the layout", "ts": ts,
			"files": []any{file}})
	}
	for _, name := range []string{"terra", "milo"} {
		envelope := envelopeWith(t, srv, name, "@terra @milo the layout")
		if !strings.Contains(envelope, "[image: layout.png · 64×32 · ") {
			t.Fatalf("%s was not shown the image:\n%s", name, envelope)
		}
		if got, err := os.ReadFile(attachmentPath(t, envelope)); err != nil || !bytes.Equal(got, shot) {
			t.Errorf("%s cannot read the image it was shown: %v", name, err)
		}
	}
	if n := slack.downloaded("F0SHOT"); n != 1 {
		t.Errorf("the shared file was downloaded %d times, want once", n)
	}

	notes := []byte("step 1\nstep 2\n")
	slack.addFile("F0NOTES", "notes.txt", "text/plain", notes)
	slack.pushEvent(t, slackAppToken, map[string]any{"type": "message", "subtype": "file_share", "channel_type": "im",
		"channel": "D0OPER", "user": slackOperator, "text": "", "ts": "1727600000.000600",
		"files": []any{map[string]any{"id": "F0NOTES", "file_access": "check_file_info"}}})
	envelope := envelopeWith(t, srv, "terra", "[file: notes.txt · 14 B · ")
	if got, err := os.ReadFile(attachmentPath(t, envelope)); err != nil || !bytes.Equal(got, notes) {
		t.Errorf("terra cannot read the file its owner sent: %v", err)
	}
}

// A loop's app shares its files as its bot user, with no bot_id on the
// event. Every other app in the channel hears the share, and must not take
// it for a person's: it would register the loop as a stranger and hold its
// file for approval.
func TestSlackALoopsFileShareIsNotAPersons(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	file := slack.addFile("F0MILO", "plan.txt", "text/plain", []byte("the plan"))

	const ts = "1727600000.000700"
	slack.pushEvent(t, slackAppToken, map[string]any{"type": "message", "subtype": "file_share", "channel_type": "channel",
		"channel": slackChannel, "user": miloBot.UserID, "text": "the plan", "ts": ts, "files": []any{file}})
	slack.waitAck(t, slackAppToken, slackAppToken+":"+slackChannel+":"+ts)
	time.Sleep(time.Second)
	if got := srv.activityWith("the plan"); len(got) != 0 {
		t.Fatalf("a loop's file share was stored as a person's message: %s", dump(got))
	}
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	for _, sender := range senders {
		if sender.SlackUserID == miloBot.UserID {
			t.Fatalf("milo's app was registered as a sender: %+v", senders)
		}
	}
	if n := slack.downloaded("F0MILO"); n != 0 {
		t.Errorf("a loop's own file was downloaded %d times", n)
	}
}

// A loop's attachment is uploaded by its app after its words, in the same
// place: the owner's DM, or the thread its words went to in the channel.
func TestSlackALoopSendsAFileAfterItsWords(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	report := []byte("all green\n")
	writeFile(t, filepath.Join(ws, "report.txt"), report)
	shot := pngOf(t, 16, 16)
	writeFile(t, filepath.Join(ws, "shots", "home.png"), shot)
	srv, slack := startSlackFleetWith(t, map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": slackOperator}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}
	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))

	if res := callSend(t, terra, map[string]any{"destination": "owner_dm", "text": "the report", "attach": "report.txt"}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	upload := slack.waitUpload(t, "report.txt")
	words := slack.waitPost(t, "the report")
	if upload.Token != slackBotToken || upload.Channel != words.Channel || upload.ThreadTS != "" ||
		!bytes.Equal(upload.Body, report) || upload.Posts < postIndex(slack, words)+1 {
		t.Errorf("the file %+v did not follow the words %+v into the owner's DM", upload, words)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if got := srv.activityWith("the report"); len(got) == 1 && got[0].Mirror == "mirrored" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the send with a file is not mirrored: %s", dump(srv.activityWith("the report")))
		}
	}

	// in the channel, a reply's words and file both go in its thread
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "screenshot please", "1727600000.000800")
	asked := srv.waitGroupMessage("screenshot please", func(activityMessage) bool { return true })
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "here it is",
		"attach": "shots/home.png", "reply_to": fmt.Sprintf("ref:%d", asked.ID)}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	shared := slack.waitUpload(t, "home.png")
	if shared.Channel != slackChannel || shared.ThreadTS != "1727600000.000800" || !bytes.Equal(shared.Body, shot) {
		t.Errorf("the screenshot was shared as %+v, want in the thread it answers", shared)
	}
}

// postIndex is where post sits among the posts the fake took.
func postIndex(slack *fakeSlack, post slackPost) int {
	for i, taken := range slack.postsTaken() {
		if taken.TS == post.TS {
			return i
		}
	}
	return -1
}
