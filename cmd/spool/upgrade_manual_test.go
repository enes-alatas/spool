package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

const testHubPID = "4242"

// operatorTerminal is the upgrade's output as the operator reads it: when
// it asks for the hub to be started, the operator starts it, at the
// version the env's hub comes up on (#1311).
type operatorTerminal struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	env   *upgradeEnv
	start func()
}

func (term *operatorTerminal) Write(written []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if strings.Contains(string(written), "start the hub now") && term.start != nil {
		term.start()
	}
	return term.buf.Write(written)
}

func (term *operatorTerminal) String() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.buf.String()
}

// newManualEnv is a hub no unit runs: started by hand as its own user from
// the binary at env.bin, holding env.dataDir's database, and upgraded by
// that user, not root.
func newManualEnv(t *testing.T) (*upgradeEnv, *upgrader, *fakeHost, *operatorTerminal) {
	t.Helper()
	env, up, host, _ := newUpgradeEnv(t)
	term := &operatorTerminal{env: env}
	term.start = func() { env.setServing(env.startsAs) }
	up.out = term
	env.setServing("v0.5.0")
	listen := strings.TrimPrefix(env.health.URL, "http://")
	up.noUnit, up.unitName = true, ""
	up.dataDir = env.dataDir
	up.euid = func() int { return 501 }
	up.uidOf = func(string) (int, error) { return 501, nil }
	up.selfExe = func() (string, error) { return env.bin, nil }
	up.exeOf = func(context.Context, int) (string, error) { return env.bin, nil }
	// ps, as on a Mac; TestUpgradeWithNoUnitReadsLinuxArgvFromProc covers /proc
	up.goos = "darwin"
	up.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	up.stopWait, up.startWait = 300*time.Millisecond, 300*time.Millisecond
	host.answer = func(cmd hostCmd) (string, error, bool) {
		switch {
		case cmd.Name == "lsof" && cmd.Args[0] == "-t":
			env.mu.Lock()
			serving := env.serving
			env.mu.Unlock()
			if serving == "" {
				return "", errors.New("exit status 1"), true
			}
			return testHubPID + "\n", nil, true
		case cmd.Name == "ps":
			return env.bin + " --data-dir " + env.dataDir + " --listen " + listen + "\n", nil, true
		case cmd.Name == "kill":
			env.setServing("")
			return "", nil, true
		case cmd.Name == "docker" && cmd.Args[0] == "info":
			return "id\n", nil, true
		case cmd.User == "" && strings.Contains(cmd.Name, "spool-upgrade-run-"):
			// the candidate, asked as the hub's own user
			switch strings.Join(cmd.Args, " ") {
			case "--version":
				return "spool " + env.candidateVersion + " (abc1234, built 2026-10-16T00:00:00Z, go1.26)\n", nil, true
			case "migrations":
				return env.migrations, nil, true
			case "images":
				return env.images, nil, true
			}
		case cmd.User == "nobody" || cmd.Name == "systemctl" || strings.HasPrefix(cmd.Name, "runuser"):
			t.Errorf("a hub with no unit ran %s", cmd)
			return "", errors.New("not here"), true
		}
		return "", nil, false
	}
	return env, up, host, term
}

func manualSummary(host *fakeHost) []string {
	var out []string
	for _, cmd := range host.commands() {
		if strings.Contains(cmd, "spool-upgrade-run-") {
			_, args, _ := strings.Cut(cmd, "spool-upgrade-run-")
			_, args, _ = strings.Cut(args, " ")
			cmd = "self " + args
		}
		out = append(out, cmd)
	}
	return out
}

var manualOrder = []string{
	"lsof -t -- %data/spool.db",
	"ps -o args= -p " + testHubPID,
	"docker info --format {{.ID}}",
	"docker image inspect --format {{.Id}} spool-workstation",
	"docker tag sha256:old-spool-workstation spool-workstation:spool-previous",
	"docker image inspect --format {{.Id}} spool-egress",
	"docker tag sha256:old-spool-egress spool-egress:spool-previous",
	"self --version",
	"self migrations",
	"self images",
	"docker pull " + testWSRef,
	"docker pull " + testEgRef,
	"lsof -t -- %data/spool.db",
	"kill -TERM " + testHubPID,
	"lsof -t -- %data/spool.db",
	"backup",
	"docker tag " + testWSRef + " spool-workstation",
	"docker tag " + testEgRef + " spool-egress",
}

func expand(env *upgradeEnv, order []string) []string {
	var out []string
	for _, line := range order {
		out = append(out, strings.ReplaceAll(line, "%data", env.dataDir))
	}
	return out
}

// A hub no unit runs is upgraded as its own user in the runbook's order:
// found by the process holding its database, stopped with SIGTERM, backed
// up and installed, and then started by the operator on the command line
// it ran with, which the upgrade names and waits for (#1311).
func TestUpgradeWithNoUnitStopsTheHubAndWaitsForTheOperator(t *testing.T) {
	env, up, host, term := newManualEnv(t)
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, term)
	}
	if got, want := manualSummary(host), expand(env, manualOrder); !slices.Equal(got, want) {
		t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if got := read(t, env.bin); got != "new spool" {
		t.Errorf("the binary holds %q; want the new build", got)
	}
	if got := read(t, env.bin+".previous"); got != "old spool" {
		t.Errorf("the previous binary holds %q; want the old build", got)
	}
	listen := strings.TrimPrefix(env.health.URL, "http://")
	if want := "start the hub now, where you started it before: " + env.bin + " --data-dir " + env.dataDir + " --listen " + listen; !strings.Contains(term.String(), want) {
		t.Errorf("the output doesn't name the start:\n%s\nwant %q", term, want)
	}
	if want := filepath.Join(filepath.Dir(env.dataDir), "spool-backups"); up.backupDir != want {
		t.Errorf("the backup went to %s; want %s beside the data directory", up.backupDir, want)
	}
}

// Without a unit the upgrade runs as the user who owns the data directory
// and runs the hub: root is refused, and so is anyone else.
func TestUpgradeWithNoUnitRunsAsTheHubsUser(t *testing.T) {
	for label, euid := range map[string]int{"root": 0, "another user": 502} {
		env, up, host, term := newManualEnv(t)
		up.euid = func() int { return euid }
		err := up.run(context.Background())
		if err == nil {
			t.Fatalf("%s: the upgrade ran\n%s", label, term)
		}
		if euid == 0 && !strings.Contains(err.Error(), "without sudo") {
			t.Errorf("%s: %v; want it told to run without sudo", label, err)
		}
		if slices.ContainsFunc(host.commands(), func(ran string) bool { return strings.HasPrefix(ran, "kill") || ran == "backup" }) {
			t.Errorf("%s: something moved: %v", label, host.commands())
		}
		if got := read(t, env.bin); got != "old spool" {
			t.Errorf("%s: the binary is %q", label, got)
		}
	}
}

// The upgrade replaces the binary it is: a hub that runs another one is
// refused, naming the one to run, and so is a database two processes hold.
func TestUpgradeWithNoUnitRefusesAHubItCantPlace(t *testing.T) {
	_, up, _, _ := newManualEnv(t)
	up.exeOf = func(context.Context, int) (string, error) { return "/opt/other/spool", nil }
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "/opt/other/spool upgrade --no-unit") {
		t.Errorf("another binary: %v; want it named", err)
	}

	_, up, host, _ := newManualEnv(t)
	inner := host.answer
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.Name == "lsof" {
			return "4242\n4343\n", nil, true
		}
		return inner(cmd)
	}
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "2 processes hold") {
		t.Errorf("two holders: %v; want a refusal", err)
	}
}

// A hub that doesn't come up on the new build within the wait is rolled
// back: stopped if it runs, the data directory restored, the previous build
// put back, and the operator told to start it again.
func TestUpgradeWithNoUnitRollsBackAFailedStart(t *testing.T) {
	env, up, host, term := newManualEnv(t)
	env.startsAs = "v0.5.0"
	err := up.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("%v; want a rollback\n%s", err, term)
	}
	data := filepath.Join(env.backupDir, "spool-data-20261016T000000Z.tar.gz")
	keys := filepath.Join(env.backupDir, "spool-keys-20261016T000000Z.tar")
	want := append(expand(env, manualOrder),
		"lsof -t -- "+env.dataDir+"/spool.db",
		"kill -TERM "+testHubPID,
		"lsof -t -- "+env.dataDir+"/spool.db",
		"restore --data "+data+" --data-dir "+env.dataDir+" --no-volumes --keys "+keys,
		"docker tag spool-workstation:spool-previous spool-workstation",
		"docker tag spool-egress:spool-previous spool-egress",
	)
	if got := manualSummary(host); !slices.Equal(got, want) {
		t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if got := read(t, env.bin); got != "old spool" {
		t.Errorf("the binary is %q after the rollback; want the previous one", got)
	}
	if strings.Count(term.String(), "start the hub now") != 2 {
		t.Errorf("the operator wasn't asked to start the previous build again:\n%s", term)
	}
}

// A hub whose user reaches no docker, such as one of bare loops on a Mac,
// is upgraded without an image step, and the plan says so.
func TestUpgradeWithNoUnitAndNoDockerLeavesTheImages(t *testing.T) {
	env, up, host, term := newManualEnv(t)
	up.lookPath = func(name string) (string, error) {
		if name == "docker" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, term)
	}
	if slices.ContainsFunc(host.commands(), func(ran string) bool { return strings.HasPrefix(ran, "docker") }) {
		t.Errorf("ran docker with none to reach: %v", host.commands())
	}
	if !strings.Contains(term.String(), "no image is kept, pulled or tagged") {
		t.Errorf("the output doesn't say the images were left:\n%s", term)
	}
	if got := read(t, env.bin); got != "new spool" {
		t.Errorf("the binary holds %q; want the new build", got)
	}
}

// A dry run of a hub with no unit names it, the hub it found, and the
// operator's own start, and changes nothing; a hub that isn't running is
// planned from the data directory and --listen.
func TestUpgradeWithNoUnitDryRunNamesTheOperatorsStart(t *testing.T) {
	env, up, host, term := newManualEnv(t)
	up.dryRun = true
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{"plan for the hub with no unit on " + env.dataDir, "hub         running: " + env.bin, "SIGTERM", "start the hub yourself:", "ask the new build, as you,"} {
		if !strings.Contains(term.String(), want) {
			t.Errorf("the plan doesn't say %q:\n%s", want, term)
		}
	}
	if slices.ContainsFunc(host.commands(), func(ran string) bool { return strings.HasPrefix(ran, "kill") }) {
		t.Errorf("a dry run signalled the hub")
	}

	env, up, _, term = newManualEnv(t)
	env.setServing("")
	up.dryRun, up.listen = true, "127.0.0.1:9191"
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("dry run, stopped: %v", err)
	}
	for _, want := range []string{"hub         not running", "health      http://127.0.0.1:9191/api/health", "--data-dir '" + env.dataDir + "' --listen 127.0.0.1:9191"} {
		if !strings.Contains(term.String(), want) {
			t.Errorf("the plan for a stopped hub doesn't say %q:\n%s", want, term)
		}
	}
}

// --no-unit is implied where there is no systemctl, and the flags of the
// two kinds don't mix.
func TestUpgradeCommandTakesNoUnitWhereThereIsNoSystemd(t *testing.T) {
	var stderr bytes.Buffer
	if code := upgradeCommand([]string{"--no-unit", "--unit", "spool", "--dry-run"}, &bytes.Buffer{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "--no-unit has none") {
		t.Errorf("--unit with --no-unit: %d, %s", code, &stderr)
	}
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		stderr.Reset()
		if code := upgradeCommand([]string{"--data-dir", "/tmp/x", "--dry-run"}, &bytes.Buffer{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "are for --no-unit") {
			t.Errorf("--data-dir with a unit: %d, %s", code, &stderr)
		}
	}
}

// On Linux the hub's command line comes from /proc argument by argument,
// so a path with a space in it stays one path, and the start the operator
// is told to type quotes it.
func TestUpgradeWithNoUnitReadsLinuxArgvFromProc(t *testing.T) {
	env, up, _, term := newManualEnv(t)
	up.goos = "linux"
	up.procDir = t.TempDir()
	listen := strings.TrimPrefix(env.health.URL, "http://")
	hook := filepath.Join(t.TempDir(), "my tools", "spool-hook")
	argv := []string{env.bin, "--data-dir", env.dataDir, "--listen", listen, "--hook-bin", hook}
	if err := os.MkdirAll(filepath.Join(up.procDir, testHubPID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up.procDir, testHubPID, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	unit, err := up.resolveManual(context.Background())
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, term)
	}
	if unit.HookBin != hook {
		t.Errorf("hook-bin %q; want %q", unit.HookBin, hook)
	}
	if want := "--hook-bin '" + hook + "'"; !strings.Contains(unit.startCommand(), want) {
		t.Errorf("start %q; want it to hold %q", unit.startCommand(), want)
	}
}

// Where ps joins the arguments with spaces, a command line whose split is
// a guess is refused before anything moves, rather than installing at a
// piece of a path.
func TestUpgradeWithNoUnitRefusesASpacedPathItCantSplit(t *testing.T) {
	for label, line := range map[string]string{
		"a spaced value":      "/opt/spool/spool --data-dir %data --hook-bin /Users/a b/spool-hook",
		"a spaced inline":     "/opt/spool/spool --data-dir=%data --hook-bin=/Users/a b/spool-hook",
		"a spaced binary":     "/Users/a b/spool --data-dir %data",
		"a value like a flag": "/opt/spool/spool --data-dir %data --hook-bin /Users/a -b/spool-hook",
	} {
		env, up, host, _ := newManualEnv(t)
		inner := host.answer
		host.answer = func(cmd hostCmd) (string, error, bool) {
			if cmd.Name == "ps" {
				return strings.ReplaceAll(line, "%data", env.dataDir) + "\n", nil, true
			}
			return inner(cmd)
		}
		err := up.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "ps can't show where its arguments split") {
			t.Errorf("%s: %v; want a refusal", label, err)
		}
		if slices.ContainsFunc(host.commands(), func(ran string) bool { return strings.HasPrefix(ran, "kill") || ran == "backup" }) {
			t.Errorf("%s: something moved: %v", label, host.commands())
		}
	}
}

// unambiguous knows the hub's flags: every flag main.go defines is in
// hubValueFlags or hubBoolFlags, by the kind it is.
func TestUnambiguousKnowsTheHubsFlags(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// only the hub's own flag set: the subcommands use their own fs
	found := regexp.MustCompile(`\bflag\.(String|Int|Bool|Duration)\("([a-z-]+)"`).FindAllStringSubmatch(string(src), -1)
	var values, bools []string
	for _, match := range found {
		if match[1] == "Bool" {
			bools = append(bools, match[2])
		} else {
			values = append(values, match[2])
		}
	}
	slices.Sort(values)
	slices.Sort(bools)
	wantValues, wantBools := slices.Sorted(slices.Values(hubValueFlags)), slices.Sorted(slices.Values(hubBoolFlags))
	if !slices.Equal(values, wantValues) {
		t.Errorf("main.go's value flags\n  %v\nhubValueFlags\n  %v", values, wantValues)
	}
	if !slices.Equal(bools, wantBools) {
		t.Errorf("main.go's bool flags\n  %v\nhubBoolFlags\n  %v", bools, wantBools)
	}
}

// On a terminal the manual start's prompt stands out: bold yellow, then a
// blinking line with the time left, cleared once the hub answers (#1311).
func TestUpgradeWithNoUnitMakesTheStartPromptStandOut(t *testing.T) {
	_, up, _, term := newManualEnv(t)
	up.attention = true
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, term)
	}
	out := term.String()
	for _, want := range []string{
		ansiAttention + "start the hub now, where you started it before: ",
		ansiClearLine + ansiAttention + "● waiting for the hub to answer, ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output has no %q:\n%q", want, out)
		}
	}
	if !strings.Contains(out, ansiReset+ansiClearLine) {
		t.Errorf("the blinking line isn't cleared:\n%q", out)
	}
	// a frame wider than a narrow terminal would wrap, and \r would redraw
	// only its last row
	frames := regexp.MustCompile(`[●○][^\x1b\n]*`).FindAllString(out, -1)
	if len(frames) == 0 {
		t.Fatalf("no frame:\n%q", out)
	}
	for _, visible := range frames {
		if utf8.RuneCountInString(visible) > 48 {
			t.Errorf("a frame is %d columns wide; want at most 48: %q", utf8.RuneCountInString(visible), visible)
		}
	}

	_, up, _, term = newManualEnv(t)
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, term)
	}
	if strings.Contains(term.String(), "\x1b[") {
		t.Errorf("output that isn't a terminal carries escapes:\n%q", term)
	}
}
