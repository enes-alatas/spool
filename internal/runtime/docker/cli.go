package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// errNotFound marks state reads against a container or volume that does not
// exist.
var errNotFound = errors.New("docker: no such object")

// cliError is one docker CLI invocation failing: the exit code and a stderr
// tail for diagnosis. Argv never carries secrets (ADR-0018), so the message
// is safe to log.
type cliError struct {
	subcommand string
	exitCode   int // -1 when the process didn't run or died on a signal
	stderr     string
	err        error
}

func (failure *cliError) Error() string {
	return fmt.Sprintf("docker %s: %v (%s)", failure.subcommand, failure.err, failure.stderr)
}

func (failure *cliError) Unwrap() error { return failure.err }

// command runs one docker CLI invocation under a bounded deadline and
// returns its stdout.
func (rt *Runtime) command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rt.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return nil, &cliError{
			subcommand: args[0],
			exitCode:   exitCode,
			stderr:     stderrTail(stderr.String()),
			err:        err,
		}
	}
	return stdout.Bytes(), nil
}

// containerState is the slice of docker inspect's .State we act on.
type containerState struct {
	Status  string `json:"Status"`
	Running bool   `json:"Running"`
	Paused  bool   `json:"Paused"`
}

func (rt *Runtime) inspectState(ctx context.Context, name string) (*containerState, error) {
	// --type container matters: the loop's volume shares the name, and an
	// unscoped inspect would resolve to it once the container is gone.
	out, err := rt.command(ctx, queryTimeout, "inspect", "--type", "container", "--format", "{{json .State}}", name)
	if err != nil {
		if notFound(err) {
			return nil, errNotFound
		}
		return nil, err
	}
	var state containerState
	if err := json.Unmarshal(bytes.TrimSpace(out), &state); err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", name, err)
	}
	return &state, nil
}

// notFound recognizes the CLI's ways of saying a container or volume does
// not exist ("No such object" from inspect, "No such container/volume" from
// the daemon).
func notFound(err error) bool {
	var failure *cliError
	if !errors.As(err, &failure) {
		return false
	}
	msg := strings.ToLower(failure.stderr)
	return strings.Contains(msg, "no such object") ||
		strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "no such volume")
}

// nameInUse recognizes a docker run losing the container name to an
// existing container.
func nameInUse(err error) bool {
	var failure *cliError
	return errors.As(err, &failure) && strings.Contains(failure.stderr, "is already in use")
}

// stderrTail keeps error messages bounded.
func stderrTail(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 512 {
		text = text[len(text)-512:]
	}
	return text
}
