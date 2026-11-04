package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// hubUnit is what `spool upgrade` reads out of the systemd unit that runs
// the hub (ADR-0057): the binary it starts and the flags that say
// where the rest is, so any layout upgrades the same (#885).
type hubUnit struct {
	Name             string
	Bin              string // ExecStart's path
	HookBin          string // --hook-bin, or spool-hook beside Bin
	GitHubBin        string // --github-bin, or spool-github beside Bin
	DataDir          string // --data-dir
	Listen           string // --listen, for the health check
	WorkstationImage string // --workstation-image, or its default
	EgressImage      string // --egress-image, or its default
	User             string // User=, "" for root

	// Manual is a hub no unit runs (#1311): started by hand, found by the
	// process that holds its database, and started again by the operator.
	Manual bool
	// Start is the command line the operator started a manual hub with,
	// as ps reads it, for them to type again; nil when none was running.
	Start []string
}

// parseUnit reads `systemctl show <unit> -p ExecStart -p User` output.
// ExecStart comes as `{ path=… ; argv[]=… ; … }`; a unit with more than
// one ExecStart, or none, is refused rather than guessed at.
func parseUnit(name, show string) (hubUnit, error) {
	unit := hubUnit{Name: name, Listen: "127.0.0.1:8080", WorkstationImage: localWorkstationImage, EgressImage: localEgressImage}
	var execStarts []string
	for line := range strings.SplitSeq(show, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		switch {
		case !ok:
		case key == "User":
			unit.User = value
		case key == "ExecStart" && value != "":
			execStarts = append(execStarts, value)
		}
	}
	if len(execStarts) != 1 {
		return hubUnit{}, fmt.Errorf("unit %s: found %d ExecStart lines; want exactly one (is the unit installed? systemctl status %s; a hub started by hand upgrades with --no-unit)", name, len(execStarts), name)
	}
	path, argv, err := parseExecStart(execStarts[0])
	if err != nil {
		return hubUnit{}, fmt.Errorf("unit %s: %w", name, err)
	}
	unit.Bin = path
	if err := unit.readFlags(argv); err != nil {
		return hubUnit{}, fmt.Errorf("unit %s: %w", name, err)
	}
	if unit.DataDir == "" {
		return hubUnit{}, fmt.Errorf("unit %s: ExecStart names no --data-dir; add it to the unit so the upgrade backs up the right directory", name)
	}
	if !filepath.IsAbs(unit.Bin) || !filepath.IsAbs(unit.DataDir) {
		return hubUnit{}, fmt.Errorf("unit %s: the binary and --data-dir must be absolute paths, got %q and %q", name, unit.Bin, unit.DataDir)
	}
	if unit.HookBin == "" {
		unit.HookBin = filepath.Join(filepath.Dir(unit.Bin), "spool-hook")
	}
	if unit.GitHubBin == "" {
		unit.GitHubBin = filepath.Join(filepath.Dir(unit.Bin), "spool-github")
	}
	return unit, nil
}

// readFlags sets the paths and names the hub's command line gives, argv[0]
// being the binary: a unit's ExecStart, or the command line a manual hub
// runs with (#1311).
func (unit *hubUnit) readFlags(argv []string) error {
	flags := map[string]*string{
		"data-dir": &unit.DataDir, "hook-bin": &unit.HookBin, "github-bin": &unit.GitHubBin, "listen": &unit.Listen,
		"workstation-image": &unit.WorkstationImage, "egress-image": &unit.EgressImage,
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		flagName, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		target, known := flags[flagName]
		if !known {
			continue
		}
		if !inline {
			if i+1 >= len(argv) {
				return fmt.Errorf("%s has no value", arg)
			}
			i++
			value = argv[i]
		}
		*target = value
	}
	return nil
}

// parseExecStart reads one ExecStart value: `{ path=/x ; argv[]=/x a b ;
// ignore_errors=no ; … }`. argv is split on spaces, which is how systemctl
// prints it; a path with a space in it isn't a layout the hub supports.
func parseExecStart(value string) (string, []string, error) {
	value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "{"), "}")
	var path string
	var argv []string
	for field := range strings.SplitSeq(value, " ; ") {
		key, rest, _ := strings.Cut(strings.TrimSpace(field), "=")
		switch key {
		case "path":
			path = rest
		case "argv[]":
			argv = strings.Fields(rest)
		}
	}
	if path == "" || len(argv) == 0 {
		return "", nil, errors.New("ExecStart has no path= or argv[]=")
	}
	return path, argv, nil
}

// installed is every file an upgrade installs, the hub binary first: the
// others may be missing from a layout older than them.
func (unit hubUnit) installed() []string { return []string{unit.Bin, unit.HookBin, unit.GitHubBin} }
