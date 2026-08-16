package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// maxLineBytes bounds a single stream-json line. Tool results can be huge;
// the default bufio.Scanner cap of 64KB is far too small.
const maxLineBytes = 10 * 1024 * 1024

// Opts describes one claude subprocess. Exactly one of SessionID (fresh
// session with a pre-chosen uuid) or ResumeID (continue an existing session)
// must be set.
type Opts struct {
	Bin                string // path to the claude binary
	WorkDir            string // must be stable across respawns: sessions are keyed by cwd
	Model              string
	Effort             string // ""|low|medium|high|xhigh|max → --effort
	AppendSystemPrompt string
	SessionID          string
	ResumeID           string
	AddDirs            []string
	PartialMessages    bool
	ExtraArgs          []string
}

type ExitInfo struct {
	Code   int
	Stderr string // tail of stderr, for diagnostics and resume-failure detection
}

// Proc is a running claude subprocess speaking stream-json on stdin/stdout.
type Proc interface {
	// Send writes one user message. Never call while a previous turn is in
	// flight; turn serialization is the caller's job.
	Send(text string) error
	// Events yields decoded stdout lines; closed when stdout closes.
	Events() <-chan Event
	// CloseStdin asks the process to finish up and exit cleanly (verified:
	// closing stdin after a result exits 0).
	CloseStdin() error
	Kill() error
	// Wait blocks until the process exits and returns its code and stderr tail.
	Wait() ExitInfo
	PID() int
}

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event

	stderrMu  sync.Mutex
	stderr    strings.Builder
	stdinMu   sync.Mutex
	stdinDone bool

	waitOnce sync.Once
	exit     ExitInfo
}

// Spawn starts a claude subprocess. It returns as soon as the process is
// started; the system/init event arrives on Events().
func Spawn(ctx context.Context, o Opts) (Proc, error) {
	if (o.SessionID == "") == (o.ResumeID == "") {
		return nil, fmt.Errorf("claude: exactly one of SessionID or ResumeID required")
	}
	args := []string{
		"-p", "--verbose", // --verbose is mandatory with -p + stream-json output
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--permission-mode", "bypassPermissions",
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	} else {
		args = append(args, "--resume", o.ResumeID)
	}
	if o.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", o.AppendSystemPrompt)
	}
	for _, d := range o.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if o.PartialMessages {
		args = append(args, "--include-partial-messages")
	}
	args = append(args, o.ExtraArgs...)

	bin := o.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = o.WorkDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude: start %s: %w", bin, err)
	}

	p := &proc{cmd: cmd, stdin: stdin, events: make(chan Event, 64)}

	go p.readStderr(stderr)
	go p.readStdout(stdout)

	// If the surrounding context dies, take the subprocess with it.
	go func() {
		<-ctx.Done()
		_ = p.Kill()
	}()

	return p, nil
}

func (p *proc) readStdout(r io.Reader) {
	defer close(p.events)
	br := bufio.NewReaderSize(r, 256*1024)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			p.events <- DecodeEvent(line)
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one \n-terminated line up to maxLineBytes; anything beyond
// is discarded (the truncated prefix is still returned so we keep evidence).
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			if len(buf) > maxLineBytes {
				// swallow the rest of this oversized line
				for err == bufio.ErrBufferFull {
					_, err = br.ReadSlice('\n')
				}
				return buf[:maxLineBytes], err
			}
			continue
		}
		return trimNewline(buf), err
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (p *proc) readStderr(r io.Reader) {
	const keep = 8 * 1024
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.stderrMu.Lock()
			p.stderr.Write(buf[:n])
			if p.stderr.Len() > keep {
				s := p.stderr.String()
				p.stderr.Reset()
				p.stderr.WriteString(s[len(s)-keep:])
			}
			p.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
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

func (p *proc) Send(text string) error {
	var m userMessage
	m.Type = "user"
	m.Message.Role = "user"
	m.Message.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdinDone {
		return fmt.Errorf("claude: stdin already closed")
	}
	_, err = p.stdin.Write(line)
	return err
}

func (p *proc) Events() <-chan Event { return p.events }

func (p *proc) CloseStdin() error {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdinDone {
		return nil
	}
	p.stdinDone = true
	return p.stdin.Close()
}

func (p *proc) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	return nil // Wait() escalates to SIGKILL if needed via process group teardown
}

func (p *proc) Wait() ExitInfo {
	p.waitOnce.Do(func() {
		err := p.cmd.Wait()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		p.stderrMu.Lock()
		tail := p.stderr.String()
		p.stderrMu.Unlock()
		p.exit = ExitInfo{Code: code, Stderr: tail}
	})
	return p.exit
}

func (p *proc) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// IsSessionNotFound reports whether an exit looks like a failed --resume
// (verified: exit code 1 + this stderr line, no stdout JSON).
func IsSessionNotFound(e ExitInfo) bool {
	return e.Code == 1 && strings.Contains(e.Stderr, "No conversation found with session ID")
}
