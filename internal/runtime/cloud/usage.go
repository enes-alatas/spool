package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/roomtext"
)

// A cloud turn's transcript carries no rate-limit event (ADR-0059), so the
// hub learns a cloud-only login's plan usage by running the CLI's own
// /usage on the login, as a cloud send runs: the token in the environment
// and a config dir of its own, so the CLI holds nothing to refresh the
// login with (#1285). /usage prints a table for people; the operator reads
// its output on every CLI bump, and a line this can't read leaves the
// usage unknown and says so.

// usageScope is the scope a sign-in needs for /usage to read its plan; a
// setup-token never holds it.
const usageScope = "user:profile"

// usageArgs runs /usage once: print mode, no tool and no MCP server, no
// session kept, and the cheapest model should the CLI ever send the line
// to one rather than run it.
var usageArgs = []string{
	"-p", "/usage",
	"--model", claude.LoginCheckModel,
	"--tools", "",
	"--strict-mcp-config",
	"--no-session-persistence",
}

// Bounds on the run: its output is a few lines.
const (
	usageMaxBytes = 64 << 10
	usageTimeout  = 30 * time.Second
)

// ErrNoUsageScope is a read on a sign-in that doesn't hold usageScope: its
// plan usage stays unknown, as before #1285.
var ErrNoUsageScope = errors.New("cloud: the sign-in has no " + usageScope + " scope, so its plan usage can't be read")

// PlanUsage reads the plan usage of the account of the Claude login called
// login, "" for the default, by running the CLI's /usage: its five-hour
// and seven-day windows as a rate-limit event carries them, fractions used
// and resets in unix seconds. A window /usage doesn't print is nil. It is
// ErrNoAccount with the login not signed in, and ErrNoUsageScope on a
// sign-in without the scope; output with neither window in it is an error
// quoting the output's start.
func (cloud *Runtime) PlanUsage(ctx context.Context, login string) (*claude.UnifiedWindows, error) {
	account, err := cloud.Account(ctx, login)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(account.Scopes, usageScope) {
		return nil, ErrNoUsageScope
	}
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	run, err := cloud.newCLIRun(ctx, login, "")
	if err != nil {
		return nil, err
	}
	defer run.remove()
	cmd := exec.CommandContext(ctx, cloud.CLI.Binary, usageArgs...)
	cmd.Dir, cmd.Env = run.dir, run.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("reading plan usage: starting %s: %w", cloud.CLI.Binary, err)
	}
	printed, readErr := io.ReadAll(io.LimitReader(stdout, usageMaxBytes+1))
	ran := cmd.Wait()
	switch {
	case readErr != nil:
		return nil, fmt.Errorf("reading plan usage: reading /usage: %w", readErr)
	case len(printed) > usageMaxBytes:
		return nil, fmt.Errorf("reading plan usage: /usage printed over %d bytes", usageMaxBytes)
	case ran != nil:
		return nil, fmt.Errorf("reading plan usage: /usage failed (%v): %s", ran,
			roomtext.Plain(snippet(bytes.TrimSpace(append(printed, stderr.Bytes()...)))))
	}
	return usageWindows(printed, time.Now())
}

// usageLine is one window's line of /usage, as CLI 2.1.322 prints it:
//
//	Current session: 23% used · resets Nov 26 at 4:50pm (Europe/Berlin)
//	Current week (all models): 14% used · resets Dec 2 at 4am (Europe/Berlin)
//
// The per-model weeks, "Current week (Fable)", are lines of their own,
// which no cap reads. A window with nothing used may print no reset.
var usageLine = regexp.MustCompile(`(?m)^[ \t]*Current (session|week \(all models\)):[ \t]*<?[ \t]*(\d+(?:\.\d+)?)%[ \t]*used(?:[ \t]*·[ \t]*resets[ \t]+(.+?)[ \t]*\(([^)]+)\))?[ \t]*$`)

// ansi is a terminal escape, which print mode should not print but costs
// nothing to drop.
var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// usageWindows reads /usage's output as a rate-limit event's windows, with
// now dating a reset that names no year. A window without a reset is nil:
// a reading is a usage and when it ends.
func usageWindows(printed []byte, now time.Time) (*claude.UnifiedWindows, error) {
	text := ansi.ReplaceAllString(string(printed), "")
	windows := &claude.UnifiedWindows{}
	found := false
	for _, match := range usageLine.FindAllStringSubmatch(text, -1) {
		found = true
		if match[3] == "" {
			continue
		}
		percent, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			return nil, fmt.Errorf("reading plan usage: a figure it can't read: %s", roomtext.Plain(match[0]))
		}
		resets, err := usageReset(match[3], match[4], now)
		if err != nil {
			return nil, fmt.Errorf("reading plan usage: %w: %s", err, roomtext.Plain(match[0]))
		}
		window := &claude.UnifiedWindow{Utilization: percent / 100, ResetsAt: resets.Unix()}
		if match[1] == "session" {
			windows.FiveHour = window
		} else {
			windows.SevenDay = window
		}
	}
	if !found {
		return nil, fmt.Errorf("reading plan usage: /usage printed no window it can read: %s",
			roomtext.Plain(snippet(bytes.TrimSpace([]byte(text)))))
	}
	return windows, nil
}

// usageReset reads a reset as /usage prints it, "Nov 26 at 4:50pm" or
// "4am", in zone. A date has no year: it is the next one at or after a
// day before now, since a reset is never long past. A time alone is the
// next one after now.
func usageReset(text, zone string, now time.Time) (time.Time, error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("a reset in a time zone it can't load (%s)", zone)
	}
	day, clock := "", text
	if before, after, ok := strings.Cut(text, " at "); ok {
		day, clock = before, after
	}
	var hour time.Time
	for _, layout := range []string{"3:04pm", "3pm", "15:04"} {
		if hour, err = time.Parse(layout, strings.ToLower(clock)); err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, errors.New("a reset time it can't read")
	}
	local := now.In(location)
	if day == "" {
		resets := time.Date(local.Year(), local.Month(), local.Day(), hour.Hour(), hour.Minute(), 0, 0, location)
		if !resets.After(now) {
			resets = resets.AddDate(0, 0, 1)
		}
		return resets, nil
	}
	date, err := time.Parse("Jan 2", day)
	if err != nil {
		return time.Time{}, errors.New("a reset date it can't read")
	}
	resets := time.Date(local.Year(), date.Month(), date.Day(), hour.Hour(), hour.Minute(), 0, 0, location)
	if resets.Before(now.Add(-24 * time.Hour)) {
		resets = resets.AddDate(1, 0, 0)
	}
	return resets, nil
}
