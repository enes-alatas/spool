package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/roomtext"
)

// errNotFound marks state reads against a container or volume that does not
// exist.
var errNotFound = errors.New("docker: no such object")

// cliError is one docker CLI invocation failing: the exit code and a stderr
// tail for diagnosis. Argv never carries secrets (ADR-0018), so the message
// is safe to log. The stderr is docker's words, so the message quotes it
// without backticks: it reaches the room in a power refusal (#959).
type cliError struct {
	subcommand string
	exitCode   int // -1 when the process didn't run or died on a signal
	stderr     string
	err        error
}

func (failure *cliError) Error() string {
	return fmt.Sprintf("docker %s: %v (%s)", failure.subcommand, failure.err, roomtext.Plain(failure.stderr))
}

func (failure *cliError) Unwrap() error { return failure.err }

// command runs one docker CLI invocation under a bounded deadline and
// returns its stdout.
func (rt *Runtime) command(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return rt.commandInput(ctx, timeout, "", args...)
}

// commandInput is command with the given stdin — how content reaches a
// workstation without crossing host argv.
func (rt *Runtime) commandInput(ctx context.Context, timeout time.Duration, stdin string, args ...string) ([]byte, error) {
	var reader io.Reader
	if stdin != "" {
		reader = strings.NewReader(stdin)
	}
	return rt.commandStream(ctx, timeout, reader, args...)
}

// commandStream is commandInput for content that is not text, or not small:
// a file streamed in without being held in memory.
func (rt *Runtime) commandStream(ctx context.Context, timeout time.Duration, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rt.bin, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
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

// containerState is the slice of docker inspect's .State we act on, the
// ID of the image the container was created from, and the posture it was
// provisioned under ("" for a container from before the posture label).
type containerState struct {
	Status  string `json:"Status"`
	Running bool   `json:"Running"`
	Paused  bool   `json:"Paused"`
	Image   string `json:"-"`
	Posture string `json:"-"`
}

func (rt *Runtime) inspectState(ctx context.Context, name string) (*containerState, error) {
	// --type container matters: the loop's volume shares the name, and an
	// unscoped inspect would resolve to it once the container is gone.
	out, err := rt.command(ctx, queryTimeout, "inspect", "--type", "container",
		"--format", `{"State":{{json .State}},"Image":{{json .Image}},"Labels":{{json .Config.Labels}}}`, name)
	if err != nil {
		if notFound(err) {
			return nil, errNotFound
		}
		return nil, err
	}
	var inspected struct {
		State  containerState
		Image  string
		Labels map[string]string
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &inspected); err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", name, err)
	}
	inspected.State.Image = inspected.Image
	inspected.State.Posture = inspected.Labels[postureLabel]
	return &inspected.State, nil
}

// imageID is the ID image names now, or errNotFound when there is no such
// image here. A rebuild under the same tag gives the tag a new ID.
func (rt *Runtime) imageID(ctx context.Context, image string) (string, error) {
	out, err := rt.command(ctx, queryTimeout, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		if notFound(err) {
			return "", errNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// notFound recognizes the CLI's ways of saying a container, volume or
// image does not exist ("No such object" from inspect, "No such
// container/volume/image" from the daemon).
func notFound(err error) bool {
	var failure *cliError
	if !errors.As(err, &failure) {
		return false
	}
	msg := strings.ToLower(failure.stderr)
	return strings.Contains(msg, "no such object") ||
		strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "no such volume") ||
		strings.Contains(msg, "no such image")
}

// networkGone recognizes the daemon's ways of saying a network does not
// exist: "No such network" from older engines, "network … not found" from
// newer ones.
func networkGone(err error) bool {
	var failure *cliError
	if !errors.As(err, &failure) {
		return false
	}
	msg := strings.ToLower(failure.stderr)
	return strings.Contains(msg, "no such network") ||
		strings.Contains(msg, "network") && strings.Contains(msg, "not found")
}

// notConnected recognizes a disconnect of a container that was never on
// the network, which leaves things as the caller wanted them.
func notConnected(err error) bool {
	var failure *cliError
	return errors.As(err, &failure) && strings.Contains(strings.ToLower(failure.stderr), "is not connected to")
}

// withPoolRemedy rewrites a network create refused for want of address
// space into what the operator does about it. Every docker loop has a
// network of its own, and a daemon's default address pools hold about 30
// networks in all.
func withPoolRemedy(err error) error {
	var failure *cliError
	if !errors.As(err, &failure) {
		return err
	}
	msg := strings.ToLower(failure.stderr)
	if !strings.Contains(msg, "address pool") && !strings.Contains(msg, "fully subnetted") {
		return err
	}
	return fmt.Errorf("docker has no address space left for this loop's network: every docker loop "+
		"has a network of its own, and the daemon's default address pools hold about 30. "+
		"Widen default-address-pools in the daemon's daemon.json, for example to "+
		`{"base":"10.201.0.0/16","size":24}, then restart docker: %w`, err)
}

// noSuchImage recognizes a docker run refused for an image that is not on
// this host: with --pull=never, the daemon says "No such image".
func noSuchImage(err error) bool {
	var failure *cliError
	return errors.As(err, &failure) && strings.Contains(strings.ToLower(failure.stderr), "no such image")
}

// withImageRemedy rewrites a run refused for a missing image into what the
// operator does about it. The hub never pulls (ADR-0052), so Docker's bare
// "No such image" leaves them to guess that the image is theirs to put
// there, and how (#766). A loop's own image (ADR-0017) is one neither
// `spool images` nor `make image` produces, so its remedy is building or
// pulling it by name. Any other error passes through unchanged.
func withImageRemedy(err error, image string, loopsOwn bool) error {
	if !noSuchImage(err) {
		return err
	}
	if loopsOwn {
		return fmt.Errorf("image %q is not in the local Docker, and the hub never pulls one: "+
			"it is this loop's own image, so build or pull it under that name, "+
			"or clear the loop's image field to use the default", image)
	}
	return fmt.Errorf("image %q is not in the local Docker, and the hub never pulls one: "+
		"run `spool images` for the commands that fetch a release's images, "+
		"or `make image` in a source checkout to build them", image)
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

// RepoDigests lists the registry references by digest that image was pulled
// as, empty for a local build: how the hub tells a release's image from
// one built here (ADR-0052).
func (rt *Runtime) RepoDigests(ctx context.Context, image string) ([]string, error) {
	out, err := rt.command(ctx, queryTimeout, "image", "inspect", "--format", "{{json .RepoDigests}}", image)
	if err != nil {
		return nil, err
	}
	var digests []string
	if err := json.Unmarshal(bytes.TrimSpace(out), &digests); err != nil {
		return nil, fmt.Errorf("docker image inspect %s: %w", image, err)
	}
	return digests, nil
}
