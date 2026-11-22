//go:build integration

package itest

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A cloud loop attaches a file from its session: its send_message's hook,
// which the setup script wrote, posts the file ahead of the call, and the
// call carries it as a contained loop's does. A file over the limit is
// refused by the hook with words naming it, and the call never reaches
// the hub; a post without the loop's token keeps nothing (ADR-0073).
func TestACloudLoopSendsAFileFromItsSession(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 here, which the cloud image has")
	}
	hub := startCloudHub(t, "cumulus")

	// over the cloud listener's 1 MB bound, under the 20 MB one
	const size = 2 << 20
	hub.message("cumulus", fmt.Sprintf("!mkfile report.txt %d\n", size)+
		`!send {"destination":"control_room","text":"the report","attach":"report.txt"}`)
	// a turn's reply is what its send was answered, or the hook's refusal
	answered := func(turn turn) bool {
		return strings.HasPrefix(turn.ResultText, "sent ") || strings.HasPrefix(turn.ResultText, "send error") ||
			strings.Contains(turn.ResultText, "send_message")
	}
	sent := hub.waitTurn("cumulus", 30*time.Second, answered)
	if sent.IsError || !strings.HasPrefix(sent.ResultText, "sent ") {
		t.Fatalf("the VM's send with a file: %+v", sent)
	}
	var activity []struct {
		Text        string           `json:"text"`
		Attachments []attachmentView `json:"attachments"`
	}
	hub.mustJSON("GET", "/api/activity", nil, &activity)
	var file *attachmentView
	for _, message := range activity {
		if message.Text == "the report" && len(message.Attachments) == 1 {
			file = &message.Attachments[0]
		}
	}
	if file == nil || file.Name != "report.txt" || file.Size != size || file.MessageID == 0 {
		t.Fatalf("the message doesn't carry the file: %s", dump(activity))
	}
	resp, body := hub.do("GET", fmt.Sprintf("/api/attachments/%d", file.ID), nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, bytes.Repeat([]byte("x"), size)) {
		t.Errorf("the kept file is %d with %d bytes, want the session's file", resp.StatusCode, len(body))
	}

	// over the limit: the hook refuses before a byte is sent
	hub.message("cumulus", fmt.Sprintf("!mkfile big.bin %d\n", 20<<20+1)+
		`!send {"destination":"control_room","text":"the big one","attach":"big.bin"}`)
	refused := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool {
		return answered(turn) && turn.ResultText != sent.ResultText
	})
	if !strings.Contains(refused.ResultText, "refused: Spool refused this file: big.bin is over the 20 MB limit") {
		t.Errorf("a file over the limit: %q, want the hook's refusal naming the limit", refused.ResultText)
	}
	if len(hub.activityWith("the big one")) != 0 {
		t.Error("a send whose file was refused was stored")
	}

	// a post without the loop's token keeps nothing
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("hook", `{"hook_event_name":"PreToolUse","tool_name":"mcp__spool__send_message","tool_use_id":"toolu_01Stranger"}`)
	_ = writer.WriteField("name", "planted.txt")
	_ = writer.WriteField("file", "planted")
	_ = writer.Close()
	req, err := http.NewRequest(http.MethodPost, hub.cloudURL+"/hooks/attach", &form)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer spool-hub-synthetic-stranger")
	req.Header.Set("X-Spool-Session", "cse_01Stranger")
	answer, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = answer.Body.Close()
	if answer.StatusCode != http.StatusUnauthorized {
		t.Errorf("a post without the loop's token: %d, want 401", answer.StatusCode)
	}
	if next, _ := hub.do("GET", fmt.Sprintf("/api/attachments/%d", file.ID+1), nil); next.StatusCode != http.StatusNotFound {
		t.Errorf("an attachment after the loop's own is %d, want none kept", next.StatusCode)
	}
}
