package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// `spool upgrade` (ADR-0057): one root-run command that upgrades the hub a
// systemd unit runs, backs it up first, and rolls it back when the new hub
// doesn't come up healthy at the version it was meant to be. Only the
// operator runs it: no route, button, tool or timer reaches it.

// releaseTag is a tag `spool upgrade` takes as a target. It is checked
// before it goes into a URL, a path or a git command.
var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// imageRef is what a candidate's `spool images` must name: a reference by
// digest, nothing else (ADR-0057).
var imageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)

const (
	upgradeRepo        = "enes-alatas/spool"
	defaultBackupDir   = "/var/backups/spool"
	defaultBackupPath  = "/usr/local/sbin/spool-backup.sh"
	defaultRestorePath = "/usr/local/sbin/spool-restore.sh"
	// maxArchive bounds a release download; a release archive is a few
	// tens of MiB.
	maxArchive = 256 << 20
)

// hostCmd is one command the upgrade runs on the host. User "" is root,
// with root's environment; any other user gets an empty one but for Env,
// so nothing of root's reaches it. Login runs Args[0] through that user's
// login shell instead, which is how a build finds the owner's Go and Node.
type hostCmd struct {
	User  string
	Login bool
	Env   []string
	Name  string
	Args  []string
}

func (cmd hostCmd) String() string {
	who := "root"
	if cmd.User != "" {
		who = cmd.User
	}
	return who + ": " + strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

// upgradeHost is every command the upgrade runs, behind one call, so the
// order it runs them in is what its tests read.
type upgradeHost interface {
	Run(ctx context.Context, cmd hostCmd) (string, error)
}

// execHost runs commands for real. Another user's command goes through
// runuser, with `env -i` so it starts from an empty environment.
type execHost struct{}

func (execHost) Run(ctx context.Context, cmd hostCmd) (string, error) {
	var c *exec.Cmd
	switch {
	case cmd.User == "":
		c = exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	case cmd.Login:
		c = exec.CommandContext(ctx, "runuser", "-l", cmd.User, "-c", cmd.Name)
	default:
		args := append([]string{"-u", cmd.User, "--", "env", "-i"}, cmd.Env...)
		c = exec.CommandContext(ctx, "runuser", append(append(args, cmd.Name), cmd.Args...)...)
	}
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			err = fmt.Errorf("%w: %s", err, said)
		}
		return string(out), fmt.Errorf("%s: %w", cmd, err)
	}
	return string(out), nil
}

// upgrader holds what one run reads and the knobs its tests turn.
type upgrader struct {
	host    upgradeHost
	http    *http.Client
	out     io.Writer
	euid    func() int
	getenv  func(string) string
	lookGh  func() (string, error)
	freeAt  func(dir string) (uint64, error)
	ownerOf func(path string) (*user.User, error)

	releaseBase string // https://github.com/<repo>/releases
	apiBase     string // https://api.github.com
	platform    string // linux_amd64
	backupDir   string
	backupPath  string
	restorePath string
	healthWait  time.Duration
	healthEvery time.Duration
	// began is when the run started, for the time each step reports
	began time.Time

	// set by the flags
	unitName           string
	target             string
	fromSource         string
	requireAttestation bool
	dryRun             bool
}

func newUpgrader(out io.Writer) *upgrader {
	return &upgrader{
		host:   execHost{},
		http:   &http.Client{Timeout: 10 * time.Minute, CheckRedirect: httpsOnly},
		out:    out,
		euid:   os.Geteuid,
		getenv: os.Getenv,
		lookGh: func() (string, error) { return exec.LookPath("gh") },
		freeAt: freeBytes,
		ownerOf: func(path string) (*user.User, error) {
			uid, err := fileOwner(path)
			if err != nil {
				return nil, err
			}
			return user.LookupId(strconv.Itoa(uid))
		},
		releaseBase: "https://github.com/" + upgradeRepo + "/releases",
		apiBase:     "https://api.github.com",
		platform:    goPlatform(),
		backupDir:   defaultBackupDir,
		backupPath:  defaultBackupPath,
		restorePath: defaultRestorePath,
		healthWait:  2 * time.Minute,
		healthEvery: 2 * time.Second,
	}
}

// httpsOnly follows a release download's redirect only to another HTTPS
// address: the bytes are checked by their sum, but nothing goes in clear.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing a redirect to %s: not https", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	return nil
}

func upgradeCommand(args []string, stdout, stderr io.Writer) int {
	up := newUpgrader(stdout)
	fs := flag.NewFlagSet("spool upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&up.unitName, "unit", "spool", "the systemd unit that runs the hub")
	fs.StringVar(&up.fromSource, "from-source", "", "build the target in this checkout, as its owner, rather than download a release")
	fs.BoolVar(&up.requireAttestation, "require-attestation", false, "refuse a release whose attestation can't be checked with gh")
	fs.BoolVar(&up.dryRun, "dry-run", false, "print the plan with the unit's paths and change nothing")
	fs.StringVar(&up.backupDir, "backup-dir", defaultBackupDir, "where the backup taken before the start goes")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sudo spool upgrade [vX.Y.Z | main] [flags]\n\nUpgrades the hub the unit runs: no target is the latest release (ADR-0057).")
		fs.PrintDefaults()
	}
	// the target may come before the flags or after them
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positional) > 1 {
		fmt.Fprintf(stderr, "spool upgrade: one target at most, got %q\n", positional)
		return 2
	}
	if len(positional) == 1 {
		up.target = positional[0]
	}
	if err := up.run(context.Background()); err != nil {
		fmt.Fprintf(stderr, "spool upgrade: %v\n", err)
		return 1
	}
	return 0
}

// plan is what a run resolved before anything changed.
type plan struct {
	unit      hubUnit
	tag       string // the release or tag, "" for main
	checkout  string // the checkout a source build runs in, "" for a release
	home      string // the checkout the unit runs its binary from, if any
	homeOwner *user.User
	// buildIsInstall: the unit runs the build checkout's own bin/spool,
	// so make build is the install (ADR-0057)
	buildIsInstall bool
	owner          *user.User
}

func (up *upgrader) printf(format string, args ...any) {
	fmt.Fprintf(up.out, format+"\n", args...)
}

// step says what the upgrade starts on now, with the time since it began,
// so a long build or backup reads as work going on rather than a hang
// (#947).
func (up *upgrader) step(format string, args ...any) {
	elapsed := time.Duration(0)
	if !up.began.IsZero() {
		elapsed = time.Since(up.began).Round(time.Second)
	}
	up.printf("spool upgrade [%s]: "+format, append([]any{elapsed}, args...)...)
}

func (up *upgrader) run(ctx context.Context) error {
	up.began = time.Now()
	if up.euid() != 0 && !up.dryRun {
		return errors.New("run it as root (sudo spool upgrade): it stops the unit, installs the binary and runs the backup")
	}
	p, err := up.resolve(ctx)
	if err != nil {
		return err
	}
	// a source build needs its tools as the owner's login shell finds
	// them, which is checked before the plan is shown or anything moves
	if up.sourceBuild() && up.euid() == 0 {
		if err := up.checkBuildTools(ctx, p); err != nil {
			return err
		}
	}
	if up.dryRun {
		up.printPlan(p)
		if up.sourceBuild() && up.euid() != 0 {
			up.printf("  (the build tools are checked as %s only under sudo)", p.owner.Username)
		}
		return nil
	}
	return up.execute(ctx, p)
}

// resolve reads the unit and the target, and checks they fit together.
// It changes nothing.
func (up *upgrader) resolve(ctx context.Context) (plan, error) {
	show, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"show", up.unitName, "-p", "ExecStart", "-p", "User"}})
	if err != nil {
		return plan{}, err
	}
	unit, err := parseUnit(up.unitName, show)
	if err != nil {
		return plan{}, err
	}
	p := plan{unit: unit}
	switch {
	case up.target == "main":
	case up.target == "" && up.fromSource != "":
		return plan{}, errors.New("--from-source builds a tag or main: name it")
	case up.target == "":
	case releaseTag.MatchString(up.target):
		p.tag = up.target
	default:
		return plan{}, fmt.Errorf("target %q is neither vX.Y.Z nor main", up.target)
	}
	if up.sourceBuild() {
		p.checkout = up.fromSource
		if p.checkout == "" {
			p.checkout = checkoutOf(unit.Bin)
		}
		if p.checkout == "" {
			return plan{}, fmt.Errorf("%s isn't in a checkout: name the one to build main in with --from-source", unit.Bin)
		}
		if p.checkout, err = filepath.Abs(p.checkout); err != nil {
			return plan{}, err
		}
		if _, err := os.Stat(filepath.Join(p.checkout, ".git")); err != nil {
			return plan{}, fmt.Errorf("%s is not a git checkout", p.checkout)
		}
	}
	if home := checkoutOf(unit.Bin); home != "" {
		p.home = home
		if p.homeOwner, err = up.ownerOf(home); err != nil {
			return plan{}, fmt.Errorf("the owner of %s: %w", home, err)
		}
		p.buildIsInstall = p.checkout == home && sameFile(unit.Bin, filepath.Join(home, "bin", "spool"))
	}
	if p.checkout != "" {
		if p.owner, err = up.ownerOf(p.checkout); err != nil {
			return plan{}, fmt.Errorf("the owner of %s: %w", p.checkout, err)
		}
	}
	return p, nil
}

// checkoutOf is the checkout a binary at <checkout>/bin/spool sits in, ""
// when it sits anywhere else.
func checkoutOf(bin string) string {
	dir := filepath.Dir(bin)
	if filepath.Base(dir) != "bin" {
		return ""
	}
	checkout := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(checkout, ".git")); err != nil {
		return ""
	}
	return checkout
}

func sameFile(a, b string) bool {
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

func (up *upgrader) printPlan(p plan) {
	u := p.unit
	target := p.tag
	switch {
	case up.target == "main":
		target = "main"
	case target == "":
		target = "the latest release"
	}
	up.printf("spool upgrade: plan for unit %s (dry run; nothing changes)", u.Name)
	up.printf("  binary      %s  (hook %s, github helper %s)", u.Bin, u.HookBin, u.GitHubBin)
	up.printf("  data        %s", u.DataDir)
	up.printf("  health      http://%s/api/health", healthHost(u.Listen))
	up.printf("  images      %s, %s", u.WorkstationImage, u.EgressImage)
	up.printf("  target      %s", target)
	steps := []string{"record the image ids " + u.WorkstationImage + " and " + u.EgressImage + " point at"}
	if up.sourceBuild() {
		steps = append(steps, fmt.Sprintf("in %s, as %s: git fetch, check out %s, make build, make image", p.checkout, p.owner.Username, target))
	} else {
		steps = append(steps, "download the "+up.platform+" archive and SHA256SUMS, check the sum and, with gh, the attestation; pull the images by digest")
	}
	steps = append(steps,
		"ask the new build, as nobody, for its version, migrations and images; refuse a downgrade onto "+filepath.Join(u.DataDir, "spool.db"),
		"check "+up.backupPath+" and "+up.restorePath+" are installed and "+up.backupDir+" has room",
		"systemctl stop "+u.Name,
		up.backupPath+" --data-dir "+u.DataDir+" --out "+up.backupDir+" --no-volumes (the upgrade changes no workstation volume)",
		"keep "+u.Bin+", "+u.HookBin+" and "+u.GitHubBin+" as *.previous, those there are",
		"install the new build there"+map[bool]string{true: " (the build is the install: the unit runs the checkout's bin/spool)", false: ""}[p.buildIsInstall],
		"systemctl start "+u.Name,
		"wait up to "+up.healthWait.String()+" for /api/health and /api/version at the target; else roll back the binary, the images and the data directory",
	)
	for i, step := range steps {
		up.printf("  %d. %s", i+1, step)
	}
}

// candidate is the new build, checked before anything stops.
type candidate struct {
	spool, hook string // files to install
	// github is spool-github, "" for a build from before it (#1054): the
	// hub runs without it, and says so.
	github  string
	version string   // what /api/version must answer
	pulls   []string // for a release: refs by digest, workstation then egress
	cleanup func()
}

// execute runs the runbook's order (ADR-0057). Every check comes before the stop,
// and a failure after the start rolls back.
func (up *upgrader) execute(ctx context.Context, p plan) error {
	u := p.unit
	// 1. the image ids, before any build or pull moves them
	recorded := map[string]string{}
	for _, name := range []string{u.WorkstationImage, u.EgressImage} {
		id, err := up.host.Run(ctx, hostCmd{Name: "docker", Args: []string{"image", "inspect", "--format", "{{.Id}}", name}})
		if err == nil {
			recorded[name] = strings.TrimSpace(id)
		}
	}
	retag := func() []error {
		var errs []error
		for _, name := range []string{u.WorkstationImage, u.EgressImage} {
			if id := recorded[name]; id != "" {
				if _, err := up.host.Run(ctx, hostCmd{Name: "docker", Args: []string{"tag", id, name}}); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errs
	}

	// in place, the build overwrites the running binary: keep it first
	if p.buildIsInstall {
		if err := up.keepPrevious(p); err != nil {
			return err
		}
	}
	// 2. the build, checked
	cand, err := up.obtain(ctx, p)
	if cand.cleanup != nil {
		defer cand.cleanup()
	}
	if err == nil {
		err = up.check(ctx, p, cand)
	}
	if err != nil {
		if up.sourceBuild() {
			// make image already moved the names: put them back
			retag()
		}
		if p.buildIsInstall {
			// make build already replaced the unit's binary: a refused
			// build must not be what the next restart starts
			if restoreErr := up.restorePrevious(p); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("putting back %s.previous: %w", p.unit.Bin, restoreErr))
			}
		}
		return err
	}

	// 3. stop
	up.step("stopping %s", u.Name)
	if _, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"stop", u.Name}}); err != nil {
		return err
	}
	// 4. back up the data directory alone: the upgrade changes no
	// workstation volume, and its rollback leaves them as they are, so
	// copying them would only keep the hub down longer (#947)
	up.step("backing up %s into %s (the workstation volumes stay as they are)", u.DataDir, up.backupDir)
	backup, err := up.host.Run(ctx, hostCmd{Name: up.backupPath, Args: []string{"--data-dir", u.DataDir, "--out", up.backupDir, "--no-volumes"}})
	dataFile, keysFile := backupFiles(backup)
	if err == nil && dataFile == "" {
		err = fmt.Errorf("%s named no data file:\n%s", up.backupPath, backup)
	}
	if err != nil {
		return up.unstarted(ctx, p, retag, false, fmt.Errorf("the backup failed, so nothing was installed: %w", err))
	}
	up.step("backup %s%s", dataFile, map[bool]string{true: ", keys " + keysFile, false: ""}[keysFile != ""])
	// 5, 6. keep the previous pair, install the new one, tag the images
	if !p.buildIsInstall {
		if err := up.keepPrevious(p); err != nil {
			return up.unstarted(ctx, p, retag, true, err)
		}
		installs := map[string]string{cand.spool: u.Bin, cand.hook: u.HookBin}
		if cand.github != "" {
			installs[cand.github] = u.GitHubBin
		}
		for src, dst := range installs {
			if err := installFile(src, dst, p.ownerIfInCheckout(dst)); err != nil {
				return up.unstarted(ctx, p, retag, true, err)
			}
		}
	}
	for i, ref := range cand.pulls {
		name := []string{u.WorkstationImage, u.EgressImage}[i]
		if _, err := up.host.Run(ctx, hostCmd{Name: "docker", Args: []string{"tag", ref, name}}); err != nil {
			return up.unstarted(ctx, p, retag, true, err)
		}
	}
	// 7, 8. start, and check
	up.step("starting %s on %s", u.Name, cand.version)
	_, err = up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"start", u.Name}})
	if err == nil {
		up.step("waiting up to %s for %s to answer at %s", up.healthWait, u.Name, cand.version)
		err = up.healthy(ctx, u, cand.version)
	}
	if err == nil {
		up.step("%s runs %s and answers healthy. The backup is %s; the previous build is %s.previous.", u.Name, cand.version, dataFile, u.Bin)
		return nil
	}
	up.step("the new hub failed its check (%v); rolling back", err)
	return up.rollback(ctx, p, dataFile, keysFile, retag, err)
}

// unstarted puts the previous build back when the new one never started:
// the database wasn't migrated, so the binary and the images are enough.
// installed says the new pair may be in place already; a build that is
// the install always is.
func (up *upgrader) unstarted(ctx context.Context, p plan, retag func() []error, installed bool, cause error) error {
	up.step("%v; putting the previous build back", cause)
	var steps []rollbackStep
	if installed || p.buildIsInstall {
		steps = append(steps, rollbackStep{"put back " + p.unit.Bin + ".previous", func() error { return up.restorePrevious(p) }})
	}
	steps = append(steps, []rollbackStep{
		{"retag the recorded images", func() error { return errors.Join(retag()...) }},
		{"systemctl start " + p.unit.Name, func() error {
			_, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"start", p.unit.Name}})
			return err
		}},
	}...)
	if err := up.runSteps(steps); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

type rollbackStep struct {
	what string
	do   func() error
}

// runSteps runs a rollback in order. A step that fails stops it, with the
// steps left printed for the operator: it never retries blindly.
func (up *upgrader) runSteps(steps []rollbackStep) error {
	for i, step := range steps {
		up.step("rollback: %s", step.what)
		if err := step.do(); err != nil {
			up.step("rollback stopped at %q: %v", step.what, err)
			up.printf("spool upgrade: left to do by hand, in order (docs/OPERATIONS.md, Rolling back):")
			for _, left := range steps[i:] {
				up.printf("  - %s", left.what)
			}
			return fmt.Errorf("the rollback stopped at %q: %w", step.what, err)
		}
	}
	return nil
}

// rollback undoes a failed upgrade (ADR-0057): the binary, the images and the data
// directory go back; the volumes stay as they are.
func (up *upgrader) rollback(ctx context.Context, p plan, dataFile, keysFile string, retag func() []error, cause error) error {
	u := p.unit
	failed := u.DataDir + ".failed-upgrade"
	if _, err := os.Lstat(failed); err == nil {
		failed += "." + time.Now().UTC().Format("20060102T150405Z")
	}
	restore := []string{"--data", dataFile, "--data-dir", u.DataDir, "--no-volumes"}
	if keysFile != "" {
		restore = append(restore, "--keys", keysFile)
	}
	steps := []rollbackStep{
		{"systemctl stop " + u.Name, func() error {
			_, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"stop", u.Name}})
			return err
		}},
		{"move " + u.DataDir + " aside as " + failed, func() error { return os.Rename(u.DataDir, failed) }},
		{up.restorePath + " " + strings.Join(restore, " "), func() error {
			_, err := up.host.Run(ctx, hostCmd{Name: up.restorePath, Args: restore})
			return err
		}},
		{"retag the recorded image ids onto " + u.WorkstationImage + " and " + u.EgressImage, func() error { return errors.Join(retag()...) }},
		{"put back " + u.Bin + ".previous, " + u.HookBin + ".previous and " + u.GitHubBin + ".previous, those there are", func() error { return up.restorePrevious(p) }},
		{"systemctl start " + u.Name, func() error {
			_, err := up.host.Run(ctx, hostCmd{Name: "systemctl", Args: []string{"start", u.Name}})
			return err
		}},
	}
	if err := up.runSteps(steps); err != nil {
		return errors.Join(cause, err)
	}
	up.step("rolled back. The failed upgrade's data directory is %s (it holds the hub's credentials: delete it once the hub is checked); the backup is %s.", failed, dataFile)
	return fmt.Errorf("the upgrade failed and was rolled back: %w", cause)
}

// ownerIfInCheckout is who a file at path belongs to: the owner of the
// checkout the unit runs from, for a file in it, so root leaves no file of
// its own there; nil, root, anywhere else.
func (p plan) ownerIfInCheckout(path string) *user.User {
	if p.home != "" && strings.HasPrefix(path, p.home+string(filepath.Separator)) {
		return p.homeOwner
	}
	return nil
}

func (up *upgrader) keepPrevious(p plan) error {
	for _, path := range p.unit.installed() {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && path != p.unit.Bin {
			continue
		}
		if err := installFile(path, path+".previous", p.ownerIfInCheckout(path)); err != nil {
			return fmt.Errorf("keeping %s: %w", path, err)
		}
	}
	return nil
}

func (up *upgrader) restorePrevious(p plan) error {
	for _, path := range p.unit.installed() {
		if _, err := os.Stat(path + ".previous"); errors.Is(err, os.ErrNotExist) && path != p.unit.Bin {
			continue
		}
		if err := installFile(path+".previous", path, p.ownerIfInCheckout(path)); err != nil {
			return err
		}
	}
	return nil
}

// installFile copies src over dst, mode 0755, through a file beside dst
// renamed into place, so the unit never starts half a binary. owner nil
// leaves it root's.
func installFile(src, dst string, owner *user.User) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	scratch, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(scratch.Name()) }()
	if _, err := io.Copy(scratch, in); err != nil {
		scratch.Close()
		return err
	}
	if err := scratch.Chmod(0o755); err != nil {
		scratch.Close()
		return err
	}
	if owner != nil {
		uid, _ := strconv.Atoi(owner.Uid)
		gid, _ := strconv.Atoi(owner.Gid)
		if err := scratch.Chown(uid, gid); err != nil {
			scratch.Close()
			return err
		}
	}
	if err := scratch.Sync(); err != nil {
		scratch.Close()
		return err
	}
	if err := scratch.Close(); err != nil {
		return err
	}
	return os.Rename(scratch.Name(), dst)
}

// backupFiles reads the two files spool-backup.sh names on its last lines.
func backupFiles(out string) (data, keys string) {
	for line := range strings.SplitSeq(out, "\n") {
		path, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch base := filepath.Base(path); {
		case strings.HasPrefix(base, "spool-data-") && strings.HasSuffix(base, ".tar.gz"):
			data = path
		case strings.HasPrefix(base, "spool-keys-") && strings.HasSuffix(base, ".tar"):
			keys = path
		}
	}
	return data, keys
}

// obtain gets the build: built in the checkout as its owner, or a
// release downloaded and checked.
func (up *upgrader) obtain(ctx context.Context, p plan) (candidate, error) {
	if up.sourceBuild() {
		return up.build(ctx, p)
	}
	return up.download(ctx, p)
}

// buildPath puts the Node and Go a version manager installed on the
// owner's PATH. The build runs in a login shell that isn't interactive,
// which reads ~/.profile, and a stock ~/.bashrc returns before the lines
// nvm and its kind add there, so an npm the owner's terminal finds is
// missing from the build (#945). Each is loaded from its default place
// when it is there, and asdf 0.16+, which has no asdf.sh, by its shims;
// nothing is installed. It holds no "&&": the order tests read a script
// from its first one.
const buildPath = `export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"; ` +
	`if [ -s "$NVM_DIR/nvm.sh" ]; then . "$NVM_DIR/nvm.sh" >/dev/null 2>&1; fi; ` +
	`for d in "$HOME/.local/share/fnm" "$HOME/.fnm"; do if [ -x "$d/fnm" ]; then eval "$("$d/fnm" env)"; fi; done; ` +
	`if [ -d "$HOME/.volta/bin" ]; then PATH="$HOME/.volta/bin:$PATH"; fi; ` +
	`if [ -s "$HOME/.asdf/asdf.sh" ]; then . "$HOME/.asdf/asdf.sh"; fi; ` +
	`if [ -d "${ASDF_DATA_DIR:-$HOME/.asdf}/shims" ]; then PATH="${ASDF_DATA_DIR:-$HOME/.asdf}/shims:$PATH"; fi; ` +
	`for d in /usr/local/go/bin "$HOME/go/bin"; do if [ -d "$d" ]; then PATH="$PATH:$d"; fi; done; export PATH; `

func (up *upgrader) asOwner(ctx context.Context, p plan, script string) (string, error) {
	return up.host.Run(ctx, hostCmd{User: p.owner.Username, Login: true, Name: buildPath + "cd " + shellWord(p.checkout) + " && " + script})
}

// buildTools is what a source build runs as the owner: make build needs
// git, make, go, node and npm, and make image a docker the owner reaches
// without sudo.
const buildTools = `for t in git make go node npm docker; do command -v "$t" >/dev/null || printf 'missing %s\n' "$t"; done; ` +
	`if command -v docker >/dev/null; then docker info >/dev/null 2>&1 || echo 'no docker'; fi`

// checkBuildTools refuses a source build whose tools the owner's login
// shell doesn't find, before the plan is shown and before anything
// stops, and says what to do about each (#945).
func (up *upgrader) checkBuildTools(ctx context.Context, p plan) error {
	out, err := up.asOwner(ctx, p, buildTools)
	if err != nil {
		return err
	}
	var missing []string
	noDocker := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case strings.HasPrefix(line, "missing "):
			missing = append(missing, strings.TrimPrefix(line, "missing "))
		case line == "no docker":
			noDocker = true
		}
	}
	who := p.owner.Username
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("the build runs as %s in a login shell, which finds no %s. "+
			"nvm, fnm, volta and asdf are loaded from their default places; a tool installed anywhere else needs its PATH set in ~/.profile, not ~/.bashrc. "+
			"Check with: sudo runuser -l %s -c 'command -v %s'", who, strings.Join(missing, ", "), who, strings.Join(missing, " ")))
	}
	if noDocker {
		problems = append(problems, fmt.Sprintf("%s can't reach docker, which make image needs: sudo usermod -aG docker %s, then log in again", who, who))
	}
	if len(problems) > 0 {
		return errors.New("nothing changed: " + strings.Join(problems, "; and "))
	}
	return nil
}

func (up *upgrader) build(ctx context.Context, p plan) (candidate, error) {
	checkout := "git fetch --tags origin && git checkout " + p.tag
	if p.tag == "" {
		checkout = "git fetch --tags origin && git checkout main && git pull --ff-only origin main"
	}
	up.step("building in %s as %s", p.checkout, p.owner.Username)
	for _, script := range []string{checkout, "make build", "make image"} {
		if script != checkout {
			up.step("%s", script)
		}
		if _, err := up.asOwner(ctx, p, script); err != nil {
			return candidate{}, err
		}
	}
	described, err := up.asOwner(ctx, p, "git describe --tags --always --dirty")
	if err != nil {
		return candidate{}, err
	}
	// The checkout's owner can write its bin/, so the pair is copied once
	// into a directory of root's, and that copy is what is checked and
	// installed: nothing can swap it between the two.
	dir, err := os.MkdirTemp("", "spool-upgrade-")
	if err != nil {
		return candidate{}, err
	}
	cand := candidate{version: strings.TrimSpace(described), cleanup: func() { _ = os.RemoveAll(dir) }}
	for _, name := range []string{"spool", "spool-hook"} {
		if err := installFile(filepath.Join(p.checkout, "bin", name), filepath.Join(dir, name), nil); err != nil {
			return cand, err
		}
	}
	cand.spool, cand.hook = filepath.Join(dir, "spool"), filepath.Join(dir, "spool-hook")
	github := filepath.Join(p.checkout, "bin", "spool-github")
	if _, err := os.Stat(github); err == nil {
		cand.github = filepath.Join(dir, "spool-github")
		if err := installFile(github, cand.github, nil); err != nil {
			return cand, err
		}
	}
	return cand, nil
}

// shellWord quotes value as one word for a shell.
func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// check asks the candidate, run as nobody, what it is: its version, the
// migrations it carries and, for a release, its images. It refuses a
// downgrade onto the data directory's database.
func (up *upgrader) check(ctx context.Context, p plan, cand candidate) error {
	dir, err := sharedCopy(cand.spool)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, filepath.Base(cand.spool))
	ask := func(args ...string) (string, error) {
		return up.host.Run(ctx, hostCmd{User: "nobody", Name: bin, Args: args})
	}

	printed, err := ask("--version")
	if fields := strings.Fields(printed); err != nil || len(fields) < 2 || fields[0] != "spool" || fields[1] != cand.version {
		return fmt.Errorf("the new build says it is %q, want spool %s (%v)", strings.TrimSpace(printed), cand.version, err)
	}

	listed, err := ask("migrations")
	if err != nil {
		return fmt.Errorf("the new build lists no migrations, so it predates spool upgrade; upgrade to it by hand (docs/OPERATIONS.md): %w", err)
	}
	carries, err := parseMigrations(listed)
	if err != nil {
		return err
	}
	applied, err := sqlite.AppliedMigrations(filepath.Join(p.unit.DataDir, "spool.db"))
	if err != nil {
		return fmt.Errorf("reading the migrations %s has applied: %w", p.unit.DataDir, err)
	}
	for _, name := range applied {
		if !slices.Contains(carries, name) {
			return fmt.Errorf("refusing a downgrade: the database has applied %s, which %s doesn't know, and no release migrates a database back; roll back from a backup instead (docs/OPERATIONS.md)", name, cand.version)
		}
	}

	if cand.pulls != nil {
		images, err := ask("images")
		if err != nil {
			return fmt.Errorf("the release names no images: %w", err)
		}
		refs, err := parseImages(images)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			up.step("pulling %s", ref)
			if _, err := up.host.Run(ctx, hostCmd{Name: "docker", Args: []string{"pull", ref}}); err != nil {
				return err
			}
		}
		copy(cand.pulls, refs)
	}

	for _, script := range []string{up.backupPath, up.restorePath} {
		if info, err := os.Stat(script); err != nil || info.Mode()&0o111 == 0 {
			return fmt.Errorf("%s isn't installed (docs/OPERATIONS.md, Back up): the upgrade backs up through it", script)
		}
	}
	if err := os.MkdirAll(up.backupDir, 0o700); err != nil {
		return err
	}
	need, err := dirSize(p.unit.DataDir)
	if err != nil {
		return err
	}
	free, err := up.freeAt(up.backupDir)
	if err != nil {
		return err
	}
	if margin := uint64(1 << 30); free < uint64(need)+margin {
		return fmt.Errorf("%s has %d MiB free; the data directory is %d MiB, and the backup needs room for it", up.backupDir, free>>20, need>>20)
	}
	return nil
}

// parseMigrations reads `spool migrations` strictly: a name a line, each
// matching the migrations' own pattern, or a refusal.
func parseMigrations(out string) ([]string, error) {
	var names []string
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if !sqlite.MigrationName.MatchString(line) {
			return nil, fmt.Errorf("the new build's migrations don't read as a list: %q", line)
		}
		names = append(names, line)
	}
	return names, nil
}

// parseImages reads `spool images` strictly: two pulls by digest,
// workstation then egress, each tagged with the hub's own name, and no
// other line.
func parseImages(out string) ([]string, error) {
	var pulls []string
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "docker" && fields[1] == "pull" && imageRef.MatchString(fields[2]):
			pulls = append(pulls, fields[2])
		case len(fields) == 4 && fields[0] == "docker" && fields[1] == "tag" && imageRef.MatchString(fields[2]) &&
			(fields[3] == localWorkstationImage || fields[3] == localEgressImage):
		default:
			return nil, fmt.Errorf("the release's images don't read as pulls by digest: %q", line)
		}
	}
	if len(pulls) != 2 || !strings.Contains(pulls[0], localWorkstationImage+"@") || !strings.Contains(pulls[1], localEgressImage+"@") {
		return nil, fmt.Errorf("the release names %d images; want the workstation's and the egress proxy's", len(pulls))
	}
	return pulls, nil
}

// sharedCopy puts a copy of bin, mode 0555, in a fresh directory of
// root's, mode 0755: nobody can run it there but not replace it, and
// doesn't need to reach where the original is (ADR-0057).
func sharedCopy(bin string) (string, error) {
	dir, err := os.MkdirTemp("", "spool-upgrade-run-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	in, err := os.Open(bin)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, filepath.Base(bin)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o555)
	if err == nil {
		_, err = io.Copy(out, in)
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// healthHost is where the health check reaches a hub listening on listen:
// an unspecified address is reached on loopback.
func healthHost(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// healthy waits for the hub to answer /api/health and to name version on
// /api/version.
func (up *upgrader) healthy(ctx context.Context, u hubUnit, version string) error {
	base := "http://" + healthHost(u.Listen)
	deadline := time.Now().Add(up.healthWait)
	var last error
	for {
		var health struct {
			OK bool `json:"ok"`
		}
		var build struct {
			Version string `json:"version"`
		}
		if err := up.getJSON(ctx, base+"/api/health", &health); err != nil {
			last = err
		} else if !health.OK {
			last = errors.New("/api/health doesn't answer ok")
		} else if err := up.getJSON(ctx, base+"/api/version", &build); err != nil {
			last = err
		} else if build.Version != version {
			last = fmt.Errorf("/api/version names %q, want %q", build.Version, version)
		} else {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("after %s: %w", up.healthWait, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(up.healthEvery):
		}
	}
}

func (up *upgrader) getJSON(ctx context.Context, url string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := up.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

// sourceBuild says the target is built from a checkout rather than
// downloaded: main always is, and a tag with --from-source.
func (up *upgrader) sourceBuild() bool { return up.target == "main" || up.fromSource != "" }
