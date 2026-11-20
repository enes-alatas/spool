package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// input is what starts a turn: a message the hub wrote on stdin, the
// background commands whose exit the CLI reports, or both in one turn.
type input struct {
	text    string
	message any    // the message's content as written, for its replay
	uuid    string // the hub's uuid for the message, "" for none
	exited  []string
	// exitsDuring are commands that exit while this turn runs: their
	// notification is emitted mid-turn, and their own turn runs next
	exitsDuring []string
	told        bool // the exited commands' notification was already emitted
	injected    bool // exitsDuring are read inside this turn, not in one of their own
}

// backgrounds are the commands "!bg <seconds>" leaves running, which
// outlive the turn that started them, as the real CLI's do while its stdin
// is open (#1709, FIDELITY.md). Each one's exit is a turn of the fake's own.
//
// A "bg-race" file in $FAKECLAUDE_STATE makes a running command exit the
// moment the hub's next message arrives, so a test can make the two meet:
// "separate" runs the command's turn first and the message's after, and
// "coalesce" runs both in one turn. The real CLI may do either (#1709).
// "during" makes the command exit while the message's turn runs: the
// notification is emitted at the exit, mid-turn, and the command's own
// turn runs after the message's, ahead of the hub's next message, which
// is how the CLI would order it if it reports an exit as it happens.
// "inject" is what 2.1.326 was seen doing with a command that exits
// mid-turn: the notification is emitted at the exit, and the turn in
// flight reads it, under a replayed user event with a uuid of the CLI's
// own, and answers it in the same result.
type backgrounds struct {
	inputs   chan input
	stateDir string

	mu      sync.Mutex
	running map[string]*time.Timer
	order   []string
	next    int
	closed  bool
}

func newBackgrounds(stateDir string) *backgrounds {
	return &backgrounds{inputs: make(chan input, 64), stateDir: stateDir, running: map[string]*time.Timer{}}
}

// start leaves a command running for the duration.
func (commands *backgrounds) start(duration time.Duration) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	commands.next++
	task := fmt.Sprintf("bg%d", commands.next)
	commands.running[task] = time.AfterFunc(duration, func() { commands.exit(task) })
	commands.order = append(commands.order, task)
}

// tasks is the level the CLI reports: every command still running.
func (commands *backgrounds) tasks() []map[string]any {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	tasks := []map[string]any{}
	for _, task := range commands.order {
		tasks = append(tasks, map[string]any{"task_id": task, "task_type": "local_bash", "description": task})
	}
	return tasks
}

// take removes the commands named, or every one for none, and returns
// them in the order they started.
func (commands *backgrounds) take(only string) []string {
	var taken, kept []string
	for _, task := range commands.order {
		if only == "" || task == only {
			commands.running[task].Stop()
			delete(commands.running, task)
			taken = append(taken, task)
		} else {
			kept = append(kept, task)
		}
	}
	commands.order = kept
	return taken
}

func (commands *backgrounds) exit(task string) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	if commands.closed {
		return
	}
	if exited := commands.take(task); len(exited) > 0 {
		commands.inputs <- input{exited: exited}
	}
}

// readStdin turns each message on stdin into an input, and ends the inputs
// at EOF: the real CLI with its stdin closed waits on no command.
func (commands *backgrounds) readStdin(stdin io.Reader) {
	in := bufio.NewScanner(stdin)
	in.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for in.Scan() {
		var msg struct {
			UUID    string `json:"uuid"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil {
			continue
		}
		var blocks []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(msg.Message.Content, &blocks)
		var text strings.Builder
		for _, block := range blocks {
			text.WriteString(block.Text)
		}
		message := input{text: text.String(), message: msg.Message.Content, uuid: msg.UUID}

		commands.mu.Lock()
		race, _ := os.ReadFile(filepath.Join(commands.stateDir, "bg-race"))
		var exited []string
		if len(commands.order) > 0 {
			switch strings.TrimSpace(string(race)) {
			case "separate":
				commands.inputs <- input{exited: commands.take("")}
			case "coalesce":
				exited = commands.take("")
			case "during":
				message.exitsDuring = commands.take("")
			case "inject":
				message.exitsDuring, message.injected = commands.take(""), true
			}
		}
		message.exited = exited
		commands.inputs <- message
		if len(message.exitsDuring) > 0 && !message.injected {
			// queued before anything the hub writes after this turn
			commands.inputs <- input{exited: message.exitsDuring, told: true}
		}
		commands.mu.Unlock()
	}
	commands.mu.Lock()
	commands.closed = true
	close(commands.inputs)
	commands.mu.Unlock()
}
