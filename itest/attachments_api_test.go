//go:build integration

package itest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The control room reads a message's attachments, serves their bytes, and
// sends one from the composer by uploading it first (#460).

type attachmentView struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	NotKept   string `json:"not_kept"`
	RemovedAt int64  `json:"removed_at"`
}

// upload sends body raw to the upload route as contentType.
func (s *server) upload(name, contentType string, body []byte) (*http.Response, []byte) {
	s.t.Helper()
	req, err := http.NewRequest("POST", s.baseURL+"/api/attachments?name="+name, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+s.operatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func (s *server) mustUpload(name string, body []byte) attachmentView {
	s.t.Helper()
	resp, raw := s.upload(name, "application/octet-stream", body)
	var kept attachmentView
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(raw, &kept) != nil || kept.ID == 0 {
		s.t.Fatalf("upload %s: %d %s", name, resp.StatusCode, raw)
	}
	return kept
}

func errorCode(body []byte) string {
	var refusal struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &refusal)
	return refusal.Code
}

func TestTheOperatorSendsALoopAFileFromTheControlRoom(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	shot := pngOf(t, 12, 6)

	kept := s.mustUpload("layout.png", shot)
	if kept.MessageID != 0 || kept.Kind != "image" || kept.Width != 12 || kept.Height != 6 || kept.Size != int64(len(shot)) {
		t.Fatalf("the upload reads %+v", kept)
	}
	resp, body := s.do("POST", "/api/loops/aster/message", map[string]any{"text": "does this look right?", "attachment_id": kept.ID})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send = %d %s", resp.StatusCode, body)
	}
	envelope := envelopeWith(t, s, "aster", "does this look right?")
	if got, err := os.ReadFile(attachmentPath(t, envelope)); err != nil || !bytes.Equal(got, shot) {
		t.Errorf("aster cannot read the file it was shown: %v\n%s", err, envelope)
	}

	var thread []struct {
		ID          int64            `json:"id"`
		Text        string           `json:"text"`
		Attachments []attachmentView `json:"attachments"`
	}
	s.mustJSON("GET", "/api/loops/aster/conversation", nil, &thread)
	if len(thread) != 1 || len(thread[0].Attachments) != 1 || thread[0].Attachments[0].ID != kept.ID ||
		thread[0].Attachments[0].MessageID != thread[0].ID {
		t.Fatalf("the conversation reads %s", dump(thread))
	}

	// one upload goes with one message
	resp, body = s.do("POST", "/api/loops/aster/message", map[string]any{"text": "and again", "attachment_id": kept.ID})
	if resp.StatusCode != http.StatusNotFound || errorCode(body) != "attachment_not_found" {
		t.Errorf("a second send of the upload = %d %s, want 404 attachment_not_found", resp.StatusCode, body)
	}
	if got := s.activityWith("and again"); len(got) != 0 {
		t.Errorf("a refused send was stored: %s", dump(got))
	}
}

// The group composer sends a file too, and every message route carries it.
func TestAFileInTheFleetChannelReachesTheLoopsItNames(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	notes := []byte("step one\n")
	kept := s.mustUpload("notes.txt", notes)

	s.mustJSON("POST", "/api/group", map[string]any{"text": "@briar read this", "attachment_id": kept.ID}, nil)
	envelope := envelopeWith(t, s, "briar", "read this")
	if !strings.Contains(envelope, "[file: notes.txt · 9 B · ") {
		t.Fatalf("briar was not shown the file:\n%s", envelope)
	}
	for _, route := range []string{"/api/group", "/api/activity"} {
		var msgs []struct {
			Text        string           `json:"text"`
			Attachments []attachmentView `json:"attachments"`
		}
		s.mustJSON("GET", route, nil, &msgs)
		found := false
		for _, m := range msgs {
			found = found || (m.Text == "@briar read this" && len(m.Attachments) == 1 && m.Attachments[0].Name == "notes.txt")
		}
		if !found {
			t.Errorf("%s does not carry the file: %s", route, dump(msgs))
		}
	}
}

// An image is served for the page to show; anything else, SVG included, is
// a download that cannot run on the hub's origin.
func TestAnAttachmentIsServedByItsKind(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	shot := pngOf(t, 4, 4)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	for _, testCase := range []struct {
		name, disposition, contentType string
		body                           []byte
	}{
		{"shot.png", "inline", "image/png", shot},
		{"evil.svg", "attachment", "application/octet-stream", svg},
		{"page.html", "attachment", "application/octet-stream", []byte("<html><script>alert(1)</script></html>")},
	} {
		kept := s.mustUpload(testCase.name, testCase.body)
		resp, body := s.do("GET", fmt.Sprintf("/api/attachments/%d", kept.ID), nil)
		header := resp.Header
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, testCase.body) ||
			!strings.HasPrefix(header.Get("Content-Disposition"), testCase.disposition) ||
			header.Get("Content-Type") != testCase.contentType || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s served %d as %q, %q", testCase.name, resp.StatusCode, header.Get("Content-Type"), header.Get("Content-Disposition"))
		}
		if testCase.disposition == "attachment" && header.Get("Content-Security-Policy") != "sandbox" {
			t.Errorf("%s served without a sandbox", testCase.name)
		}
	}
	if resp, body := s.do("GET", "/api/attachments/999", nil); resp.StatusCode != http.StatusNotFound || errorCode(body) != "attachment_not_found" {
		t.Errorf("an unknown attachment = %d %s", resp.StatusCode, body)
	}
}

// The upload route takes the file raw, and only there, and only up to the
// limit.
func TestTheUploadRouteRefusals(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, testCase := range []struct {
		why, name, contentType string
		body                   []byte
		status                 int
		code                   string
	}{
		{"a form's content type", "a.txt", "text/plain", []byte("x"), http.StatusUnsupportedMediaType, ""},
		{"a multipart form", "a.txt", "multipart/form-data; boundary=x", []byte("x"), http.StatusUnsupportedMediaType, ""},
		{"no name", "", "application/octet-stream", []byte("x"), http.StatusBadRequest, "attachment_name_required"},
		{"a JSON body", "a.json", "application/json", []byte(`{"a":1}`), http.StatusUnsupportedMediaType, ""},
		{"no content type", "a.bin", "", nil, http.StatusUnsupportedMediaType, ""},
		{"an empty file", "a.bin", "application/octet-stream", nil, http.StatusBadRequest, "attachment_empty"},
		{"over 20 MB", "big.bin", "application/octet-stream", make([]byte, 20<<20+1), http.StatusRequestEntityTooLarge, "attachment_too_large"},
	} {
		resp, body := s.upload(testCase.name, testCase.contentType, testCase.body)
		if resp.StatusCode != testCase.status || (testCase.code != "" && errorCode(body) != testCase.code) {
			t.Errorf("%s: %d %s, want %d %s", testCase.why, resp.StatusCode, body, testCase.status, testCase.code)
		}
	}
	if kept := keptFiles(t, s); len(kept) != 0 {
		t.Errorf("a refused upload was kept: %v", kept)
	}

	// raw bytes go nowhere else
	req, _ := http.NewRequest("POST", s.baseURL+"/api/group", bytes.NewReader([]byte(`{"text":"hi"}`)))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+s.operatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("an octet-stream body to another route = %d, want 415", resp.StatusCode)
	}
}
