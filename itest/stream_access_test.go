//go:build integration

package itest

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseStream is an open /api/stream as one session reads it: every data
// line it has carried so far.
type sseStream struct {
	mu   sync.Mutex
	seen strings.Builder
	done chan struct{}
}

// openStream opens the hub's stream as session, until the test ends.
func (s *server) openStream(session string) *sseStream {
	s.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/api/stream", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Cookie", "spool_operator="+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		s.t.Fatalf("open stream: %v %v", resp, err)
	}
	stream := &sseStream{done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				stream.mu.Lock()
				stream.seen.WriteString(data + "\n")
				stream.mu.Unlock()
			}
		}
	}()
	s.t.Cleanup(func() {
		cancel()
		resp.Body.Close()
		<-stream.done
	})
	return stream
}

func (stream *sseStream) has(text string) bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return strings.Contains(stream.seen.String(), text)
}

// waitFor blocks until the stream has carried text.
func (stream *sseStream) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if stream.has(text) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the stream never carried %q", text)
}

// Several members' streams each carry the private messages their user
// reads and no other (ADR-0050), with the check made outside the bus's
// publish (#721): a link made while a stream is open counts from its next
// message, and two streams of one user both follow it.
func TestMembersStreamsCarryOnlyTheirOwnPrivateMessages(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8280, First: "Operator", Username: "operator"}
	alex := user{ID: 8281, First: "Alex", Username: "alex"}
	blair := user{ID: 8282, First: "Blair", Username: "blair"}
	srv, tg := startTelegramFleet(t, operator)
	mia := srv.signedInAs("mia", "member")
	bea := srv.signedInAs("bea", "member")
	srv.confirmLink(tg, "beta", alex, mia, "mia")

	streams := map[string]*sseStream{"mia's first": srv.openStream(mia), "mia's second": srv.openStream(mia),
		"bea's": srv.openStream(bea)}
	srv.message("alpha", "every stream is open")
	for _, stream := range streams {
		stream.waitFor(t, "every stream is open")
	}

	srv.confirmLink(tg, "beta", blair, bea, "bea")
	said := map[user]string{alex: "alex's private word", blair: "blair's private word", operator: "the owner's private word"}
	for person, text := range said {
		tg.dm("alpha", person, text)
		srv.waitForMessage(text)
	}
	srv.message("alpha", "every private word is in")
	for name, stream := range streams {
		stream.waitFor(t, "every private word is in")
		reads := map[string]bool{said[alex]: strings.HasPrefix(name, "mia"), said[blair]: name == "bea's"}
		for _, text := range said {
			if got := stream.has(text); got != reads[text] {
				t.Errorf("%s stream carries %q: %v; want %v", name, text, got, reads[text])
			}
		}
	}
}
