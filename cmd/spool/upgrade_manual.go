package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A hub no systemd unit runs (#1311, ADR-0057's amendment of 2026-11-04):
// one the operator started by hand, such as `./bin/spool` on a Mac. The
// upgrade finds it by the one process holding its database open, reads
// the paths from that process's command line as it reads them from a
// unit's, stops it as systemd would, with SIGTERM, and, since it can't
// start a process in the operator's terminal, names the command for the
// operator to type and waits for the hub to come up on it.

// manualStopWait is how long a manual hub has to exit after SIGTERM, as
// systemd's default TimeoutStopSec gives a unit.
const manualStopWait = 90 * time.Second

// resolveManual reads the hub from its data directory and, when it runs,
// from its process. It changes nothing.
func (up *upgrader) resolveManual(ctx context.Context) (hubUnit, error) {
	dataDir, err := filepath.Abs(up.dataDir)
	if err != nil {
		return hubUnit{}, err
	}
	if _, err := os.Stat(filepath.Join(dataDir, "spool.db")); err != nil {
		return hubUnit{}, fmt.Errorf("%s holds no spool.db: name the hub's data directory with --data-dir", dataDir)
	}
	self, err := up.selfExe()
	if err != nil {
		return hubUnit{}, fmt.Errorf("finding this binary: %w", err)
	}
	unit := hubUnit{Manual: true, Bin: self, Listen: up.listen, WorkstationImage: localWorkstationImage, EgressImage: localEgressImage}
	pid, err := up.hubPID(ctx, dataDir)
	if err != nil {
		return hubUnit{}, err
	}
	if pid != 0 {
		exe, err := up.exeOf(ctx, pid)
		if err != nil {
			return hubUnit{}, fmt.Errorf("reading which binary the hub (pid %d) runs: %w", pid, err)
		}
		if !sameFile(exe, self) {
			return hubUnit{}, fmt.Errorf("the hub (pid %d) runs %s, not this binary %s: run that one's upgrade, `%s upgrade --no-unit --data-dir %s`", pid, exe, self, exe, dataDir)
		}
		argv, err := up.argvOf(ctx, pid)
		if err != nil {
			return hubUnit{}, fmt.Errorf("reading the hub's (pid %d) command line: %w", pid, err)
		}
		unit.Start = argv
		if err := unit.readFlags(unit.Start); err != nil {
			return hubUnit{}, fmt.Errorf("the hub's command line: %w", err)
		}
		for _, path := range []*string{&unit.HookBin, &unit.GitHubBin} {
			if *path != "" && !filepath.IsAbs(*path) {
				return hubUnit{}, fmt.Errorf("the hub runs with a relative path %q, which the upgrade can't place: start it with absolute paths", *path)
			}
		}
	}
	// the database the process holds is this one, whatever its flag says
	unit.DataDir = dataDir
	if unit.HookBin == "" {
		unit.HookBin = filepath.Join(filepath.Dir(unit.Bin), "spool-hook")
	}
	if unit.GitHubBin == "" {
		unit.GitHubBin = filepath.Join(filepath.Dir(unit.Bin), "spool-github")
	}
	return unit, nil
}

// hubPID is the process holding dataDir's database open, 0 for none. More
// than one is refused: which is the hub is a guess.
func (up *upgrader) hubPID(ctx context.Context, dataDir string) (int, error) {
	if _, err := up.lookPath("lsof"); err != nil {
		return 0, errors.New("the upgrade finds a hub with no unit through lsof, which isn't installed")
	}
	db := filepath.Join(dataDir, "spool.db")
	out, err := up.host.Run(ctx, hostCmd{Name: "lsof", Args: []string{"-t", "--", db}})
	// lsof exits 1 when nothing holds the file
	if err != nil && strings.TrimSpace(out) != "" {
		return 0, err
	}
	var pids []int
	for _, field := range strings.Fields(out) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			return 0, fmt.Errorf("lsof named %q as a process holding %s", field, db)
		}
		if !slices.Contains(pids, pid) {
			pids = append(pids, pid)
		}
	}
	switch len(pids) {
	case 0:
		return 0, nil
	case 1:
		return pids[0], nil
	}
	return 0, fmt.Errorf("%d processes hold %s open (%v): stop all but the hub, or stop the hub too and run the upgrade again", len(pids), db, pids)
}

// exeOfDefault is the binary a process runs: /proc's link on Linux, lsof's text
// entry elsewhere.
func (up *upgrader) exeOfDefault(ctx context.Context, pid int) (string, error) {
	if up.goos == "linux" {
		return os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	}
	out, err := up.host.Run(ctx, hostCmd{Name: "lsof", Args: []string{"-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn"}})
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if path, ok := strings.CutPrefix(line, "n"); ok && filepath.IsAbs(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("lsof names no binary for pid %d", pid)
}

// stopManual stops the hub holding the database now, re-read rather than
// remembered so no other process gets the signal, and waits for the
// database to be free.
func (up *upgrader) stopManual(ctx context.Context, unit hubUnit) error {
	pid, err := up.hubPID(ctx, unit.DataDir)
	if err != nil {
		return err
	}
	if pid == 0 {
		up.step("the hub isn't running")
		return nil
	}
	exe, err := up.exeOf(ctx, pid)
	if err != nil || !sameFile(exe, unit.Bin) {
		return fmt.Errorf("pid %d holds %s but doesn't run %s (%v): stop it yourself, then run the upgrade again", pid, unit.DataDir, unit.Bin, err)
	}
	up.step("stopping the hub (pid %d) with SIGTERM", pid)
	if _, err := up.host.Run(ctx, hostCmd{Name: "kill", Args: []string{"-TERM", strconv.Itoa(pid)}}); err != nil {
		return err
	}
	deadline := time.Now().Add(up.stopWait)
	for {
		held, err := up.hubPID(ctx, unit.DataDir)
		if err != nil {
			return err
		}
		if held == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the hub (pid %d) still holds %s after %s", held, unit.DataDir, up.stopWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(up.healthEvery):
		}
	}
}

// startCommand is what the operator types to start a manual hub: the
// command line it ran with, or the binary on the data directory when none
// ran.
func (unit hubUnit) startCommand() string {
	if len(unit.Start) > 0 {
		words := make([]string, len(unit.Start))
		for i, arg := range unit.Start {
			words[i] = shellArg(arg)
		}
		return strings.Join(words, " ")
	}
	return shellWord(unit.Bin) + " --data-dir " + shellWord(unit.DataDir) + " --listen " + unit.Listen
}

// stopHub and startHub are the unit's systemctl, or a manual hub's signal
// and the operator's own start.
func (up *upgrader) stopHub(ctx context.Context, unit hubUnit) error {
	if unit.Manual {
		return up.stopManual(ctx, unit)
	}
	_, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"stop", unit.Name}})
	return err
}

func (up *upgrader) startHub(ctx context.Context, unit hubUnit) error {
	if unit.Manual {
		if up.attention {
			up.step(ansiAttention+"start the hub now, where you started it before: %s"+ansiReset, unit.startCommand())
		} else {
			up.step("start the hub now, where you started it before: %s", unit.startCommand())
		}
		return nil
	}
	_, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"start", unit.Name}})
	return err
}

// stopWhat and startWhat name those steps for the plan and the rollback.
func (unit hubUnit) stopWhat() string {
	if unit.Manual {
		return "stop the hub holding " + filepath.Join(unit.DataDir, "spool.db") + " with SIGTERM, and wait for it to let go"
	}
	return "systemctl stop " + unit.Name
}

func (unit hubUnit) startWhat() string {
	if unit.Manual {
		return "start the hub yourself: " + unit.startCommand()
	}
	return "systemctl start " + unit.Name
}

// label is how the output names the hub.
func (unit hubUnit) label() string {
	if unit.Manual {
		return "the hub"
	}
	return unit.Name
}

// asSelf says the upgrade runs as the hub's own user rather than as root,
// which is a manual hub's case: every command runs as that user, with its
// environment.
func (up *upgrader) asSelf() bool { return up.noUnit && up.euid() != 0 }

// checkManualUser refuses a run as anyone but the data directory's owner:
// a manual hub runs as that user, so root would add nothing, and would
// leave its own files in the operator's directories (ADR-0057's amendment).
func (up *upgrader) checkManualUser(unit hubUnit) error {
	uid, err := up.uidOf(unit.DataDir)
	if err != nil {
		return err
	}
	if euid := up.euid(); euid != uid {
		owner := strconv.Itoa(uid)
		if account, err := up.ownerOf(unit.DataDir); err == nil {
			owner = account.Username
		}
		if euid == 0 {
			return fmt.Errorf("a hub with no unit is upgraded as the user who runs it: run it as %s, without sudo", owner)
		}
		return fmt.Errorf("run it as %s, who owns %s and runs the hub", owner, unit.DataDir)
	}
	return nil
}

// argvOfDefault is the command line a process runs with. Linux keeps it
// argument by argument in /proc. Elsewhere ps prints it joined by spaces,
// so a path with a space in it can't be told from two arguments; that
// command line is refused rather than split wrong, since the upgrade
// installs at the paths it names.
func (up *upgrader) argvOfDefault(ctx context.Context, pid int) ([]string, error) {
	if up.goos == "linux" {
		raw, err := os.ReadFile(filepath.Join(up.procDir, strconv.Itoa(pid), "cmdline"))
		if err != nil {
			return nil, err
		}
		argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		if len(argv) == 0 || argv[0] == "" {
			return nil, fmt.Errorf("pid %d has an empty command line", pid)
		}
		return argv, nil
	}
	out, err := up.host.Run(ctx, hostCmd{Name: "ps", Args: []string{"-o", "args=", "-p", strconv.Itoa(pid)}})
	if err != nil {
		return nil, err
	}
	argv := strings.Fields(out)
	if err := unambiguous(argv); err != nil {
		return nil, fmt.Errorf("%w: ps can't show where its arguments split, so restart the hub with no spaces in its paths, or upgrade by hand (OPERATIONS)", err)
	}
	return argv, nil
}

// hubValueFlags and hubBoolFlags are the hub's flags (main.go), which a
// test holds in step with it.
var (
	hubValueFlags = []string{
		"listen", "mcp-listen", "mcp-port", "cloud-listen", "trusted-host", "data-dir",
		"claude-bin", "hook-bin", "github-bin", "runtime", "workstation-image", "egress-image",
		"egress-allow", "workstation-health-sec", "problem-down-grace-sec", "exit-grace-sec",
		"telegram-api-base", "slack-api-base", "cloud-api-base", "github-api-base",
		"prompts-chat-base", "cloud-start-within-sec", "telegram-answer-timeout-sec",
		"telegram-bind-settle-sec", "events-retention-days",
	}
	hubBoolFlags = []string{
		"allow-bare", "partial-messages", "cloud-loopback-address", "forget-secrets", "version",
	}
)

// unambiguous says whether a command line split on spaces is the one the
// hub ran with. The hub takes flags only and refuses one it doesn't know,
// so a word that is neither a known flag nor a value flag's value is a
// piece of a spaced path: the binary's, or a flag's value.
func unambiguous(argv []string) error {
	if len(argv) == 0 {
		return errors.New("the hub's command line is empty")
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch {
		case !strings.HasPrefix(arg, "-"):
			return fmt.Errorf("the hub's command line has %q where a flag goes, which may be part of a path with a space in it", arg)
		case slices.Contains(hubBoolFlags, name):
		case slices.Contains(hubValueFlags, name):
			if !inline {
				i++
			}
		default:
			return fmt.Errorf("the hub's command line has %q, which isn't one of its flags", arg)
		}
	}
	return nil
}

// shellArg is value as a shell word: bare when it holds nothing the shell
// reads, single-quoted otherwise.
func shellArg(value string) string {
	if value != "" && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=,@+%") == "" {
		return value
	}
	return shellWord(value)
}

// The manual start's prompt is the one step the operator must act on, so
// on a terminal it stands out: bold yellow, then a line that blinks below
// it with the time left until the rollback, until the hub answers (#1311).
const (
	ansiAttention = "\x1b[1;33m"
	ansiDim       = "\x1b[2m"
	ansiReset     = "\x1b[0m"
	ansiClearLine = "\r\x1b[2K"
	blinkEvery    = 500 * time.Millisecond
)

// colourTerminal says whether out is a terminal that takes colour: not a
// pipe or a file, not TERM=dumb, and no NO_COLOR (no-color.org).
func colourTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// blinkStart redraws one line under the prompt until the returned stop is
// called, which clears it. The line is short and fixed, never the start
// command, which the prompt above already shows: a line wider than the
// terminal wraps, and \r then redraws only its last row, adding a row
// each frame.
func (up *upgrader) blinkStart(unit hubUnit, wait time.Duration) (stop func()) {
	deadline := time.Now().Add(wait)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(blinkEvery)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			left := max(time.Until(deadline).Round(time.Second), 0)
			style, mark := ansiAttention, "●"
			if frame%2 == 1 {
				style, mark = ansiDim, "○"
			}
			fmt.Fprintf(up.out, "%s%s%s waiting for the hub to answer, %s left%s", ansiClearLine, style, mark, left, ansiReset)
			select {
			case <-done:
				fmt.Fprint(up.out, ansiClearLine)
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}
