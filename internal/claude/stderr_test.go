package claude

import (
	"io"
	"strings"
	"testing"
	"time"
)

// slowStderr gives its text only after a delay, as a pipe still holding a
// process's last stderr does when the process has already exited.
type slowStderr struct {
	text string
	sent bool
}

func (slow *slowStderr) Read(buf []byte) (int, error) {
	if slow.sent {
		return 0, io.EOF
	}
	time.Sleep(50 * time.Millisecond)
	slow.sent = true
	return copy(buf, slow.text), nil
}

// An exit is classified by its stderr, so the tail is read only once stderr
// has ended: a lost session's "No conversation found" still in the pipe
// would otherwise read as a resume that died for no reason (#773).
func TestAwaitStderrWaitsForStderrToEnd(t *testing.T) {
	said := "No conversation found with session ID: 1234\n"
	stream := Attach(nopWriteCloser{}, strings.NewReader(""), &slowStderr{text: said})
	stream.AwaitStderr(5 * time.Second)
	if got := stream.StderrTail(); got != said {
		t.Errorf("the tail after AwaitStderr = %q, want %q", got, said)
	}
}

// A stderr some child holds open is waited on no longer than the bound.
func TestAwaitStderrIsBounded(t *testing.T) {
	held, _ := io.Pipe()
	stream := Attach(nopWriteCloser{}, strings.NewReader(""), held)
	start := time.Now()
	stream.AwaitStderr(50 * time.Millisecond)
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("AwaitStderr on a stderr held open waited %s, want about its bound", waited)
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(buf []byte) (int, error) { return len(buf), nil }
func (nopWriteCloser) Close() error                  { return nil }
