package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// maxLineBytes bounds a single stream-json line. Tool results can be huge;
// the default bufio.Scanner cap of 64KB is far too small.
const maxLineBytes = 10 * 1024 * 1024

// Stream is the stream-json conversation with a running claude process: it
// writes user messages to stdin, decodes stdout into Events, and keeps a tail
// of stderr for diagnostics. It owns no process — starting, killing and
// reaping claude is the SandboxRuntime implementation's job, and every
// runtime speaks this same protocol over whatever pipes it has.
type Stream struct {
	stdin  io.WriteCloser
	events chan Event

	stderrMu sync.Mutex
	stderr   strings.Builder

	stdinMu   sync.Mutex
	stdinDone bool
}

// Attach starts pumping a running claude process's pipes. Events() closes
// when stdout closes.
func Attach(stdin io.WriteCloser, stdout, stderr io.Reader) *Stream {
	stream := &Stream{stdin: stdin, events: make(chan Event, 64)}
	go stream.readStderr(stderr)
	go stream.readStdout(stdout)
	return stream
}

// Send writes one user message. Never call while a previous turn is in
// flight; turn serialization is the caller's job.
func (stream *Stream) Send(text string) error {
	var msg userMessage
	msg.Type = "user"
	msg.Message.Role = "user"
	msg.Message.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	stream.stdinMu.Lock()
	defer stream.stdinMu.Unlock()
	if stream.stdinDone {
		return fmt.Errorf("claude: stdin already closed")
	}
	_, err = stream.stdin.Write(line)
	return err
}

// Events yields decoded stdout lines; closed when stdout closes.
func (stream *Stream) Events() <-chan Event { return stream.events }

// CloseStdin asks claude to finish up and exit cleanly (verified: closing
// stdin after a result exits 0).
func (stream *Stream) CloseStdin() error {
	stream.stdinMu.Lock()
	defer stream.stdinMu.Unlock()
	if stream.stdinDone {
		return nil
	}
	stream.stdinDone = true
	return stream.stdin.Close()
}

// StderrTail returns what has been collected of stderr so far.
func (stream *Stream) StderrTail() string {
	stream.stderrMu.Lock()
	defer stream.stderrMu.Unlock()
	return stream.stderr.String()
}

func (stream *Stream) readStdout(stdout io.Reader) {
	defer close(stream.events)
	reader := bufio.NewReaderSize(stdout, 256*1024)
	for {
		line, err := readLine(reader)
		if len(line) > 0 {
			stream.events <- DecodeEvent(line)
		}
		if err != nil {
			return
		}
	}
}

func (stream *Stream) readStderr(stderr io.Reader) {
	const keep = 8 * 1024
	buf := make([]byte, 4096)
	for {
		count, err := stderr.Read(buf)
		if count > 0 {
			stream.stderrMu.Lock()
			stream.stderr.Write(buf[:count])
			if stream.stderr.Len() > keep {
				tail := stream.stderr.String()
				stream.stderr.Reset()
				stream.stderr.WriteString(tail[len(tail)-keep:])
			}
			stream.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one \n-terminated line up to maxLineBytes; anything beyond
// is discarded (the truncated prefix is still returned so we keep evidence).
func readLine(reader *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			if len(buf) > maxLineBytes {
				// swallow the rest of this oversized line
				for err == bufio.ErrBufferFull {
					_, err = reader.ReadSlice('\n')
				}
				return buf[:maxLineBytes], err
			}
			continue
		}
		return trimNewline(buf), err
	}
}

func trimNewline(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}
