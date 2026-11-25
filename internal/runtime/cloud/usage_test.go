package cloud

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// profileAccount is testAccount with the scope /usage needs.
var profileAccount = Account{Token: testAccount.Token, OrgUUID: testAccount.OrgUUID, AccountUUID: testAccount.AccountUUID,
	Scopes: []string{"user:inference", "user:profile", "user:sessions:claude_code"}}

// usagePrinted is /usage's output as CLI 2.1.322 printed it on the
// operator's machine on 2026-11-26, the figures kept.
const usagePrinted = `You are currently using your subscription to power your Claude Code usage

Current session: 23% used · resets Nov 26 at 4:50pm (Europe/Berlin)
Current week (all models): 14% used · resets Dec 2 at 4am (Europe/Berlin)
Current week (Fable): 10% used · resets Dec 2 at 4am (Europe/Berlin)

What's contributing to your usage?
Approximate · sessions on this machine only · shares overlap
`

// usageRuntime is a cloud runtime whose CLI is cli, a shell script, signed
// in as account.
func usageRuntime(t *testing.T, cli string, account Account) *Runtime {
	t.Helper()
	cloud := cliRuntime(t, cli)
	cloud.Account = func(context.Context, string) (Account, error) { return account, nil }
	return cloud
}

// TestPlanUsageRunsTheCLIsUsage: /usage runs as a cloud send's CLI does,
// on the account's token in the environment and a config dir of its own,
// with no tool and no MCP server, and its two windows come back as a
// rate-limit event's fractions and resets.
func TestPlanUsageRunsTheCLIsUsage(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	cli := `{ printf '%s\n' "$@"; echo "token=$CLAUDE_CODE_OAUTH_TOKEN"; echo "config=$CLAUDE_CONFIG_DIR"; } > ` + seen + `
cat <<'EOF'
` + usagePrinted + `EOF
`
	cloud := usageRuntime(t, cli, profileAccount)
	windows, err := cloud.PlanUsage(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if windows.FiveHour == nil || windows.FiveHour.Utilization != 0.23 {
		t.Errorf("five_hour = %+v, want 0.23", windows.FiveHour)
	} else if got := time.Unix(windows.FiveHour.ResetsAt, 0).In(berlin); got.Month() != time.November || got.Day() != 26 || got.Hour() != 16 || got.Minute() != 50 {
		t.Errorf("five_hour resets %v, want Nov 26 16:50 in Berlin", got)
	}
	if windows.SevenDay == nil || windows.SevenDay.Utilization != 0.14 {
		t.Errorf("seven_day = %+v, want 0.14 (all models, not Fable's 0.10)", windows.SevenDay)
	}

	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	if strings.Join(lines[:2], " ") != "-p /usage" || !strings.Contains(string(raw), "--tools\n\n--strict-mcp-config\n") {
		t.Errorf("the CLI ran with %q, want -p /usage with no tool and no MCP server", lines)
	}
	if !strings.Contains(string(raw), "token="+profileAccount.Token+"\n") || strings.Contains(string(raw), "config=\n") {
		t.Errorf("the CLI ran without the account's token or a config dir of its own: %q", raw)
	}
}

// TestPlanUsageNeedsTheProfileScope: a sign-in without the scope, as a
// setup-token or one kept before the hub kept scopes, runs nothing.
func TestPlanUsageNeedsTheProfileScope(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	cloud := usageRuntime(t, "touch "+ran+"\n", testAccount)
	if _, err := cloud.PlanUsage(context.Background(), ""); !errors.Is(err, ErrNoUsageScope) {
		t.Fatalf("err = %v, want ErrNoUsageScope", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the CLI ran for a sign-in without the scope")
	}
}

// TestPlanUsageFailsLoudly: a CLI that fails, or prints nothing this can
// read, is an error that quotes what it printed, so the log says why.
func TestPlanUsageFailsLoudly(t *testing.T) {
	for name, cli := range map[string]string{
		"exit 1":            "echo 'Invalid API key' >&2\nexit 1\n",
		"no window printed": "echo '/usage is only available for subscription plans'\n",
	} {
		cloud := usageRuntime(t, cli, profileAccount)
		_, err := cloud.PlanUsage(context.Background(), "")
		if err == nil || !strings.Contains(err.Error(), "reading plan usage") {
			t.Errorf("%s: err = %v, want a read failure", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "Invalid API key") && !strings.Contains(err.Error(), "only available") {
			t.Errorf("%s: err = %v, want it to quote the CLI", name, err)
		}
	}
}

func TestUsageWindowsReadEachShape(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, berlin)
	at := func(year int, month time.Month, day, hour, minute int) int64 {
		return time.Date(year, month, day, hour, minute, 0, 0, berlin).Unix()
	}
	for name, test := range map[string]struct {
		printed            string
		now                time.Time
		fiveHour, sevenDay *float64
		fiveResets         int64
	}{
		"the operator's": {printed: usagePrinted, now: now, fiveHour: ptr(0.23), sevenDay: ptr(0.14), fiveResets: at(2026, 11, 26, 16, 50)},
		"under one percent": {printed: "Current session: <1% used · resets Nov 26 at 4:50pm (Europe/Berlin)\n", now: now,
			fiveHour: ptr(0.01), fiveResets: at(2026, 11, 26, 16, 50)},
		"terminal colour": {printed: "\x1b[1mCurrent session:\x1b[0m 5% used · resets Nov 26 at 4:50pm (Europe/Berlin)\n", now: now,
			fiveHour: ptr(0.05), fiveResets: at(2026, 11, 26, 16, 50)},
		"a time alone, later today": {printed: "Current session: 5% used · resets 4:50pm (Europe/Berlin)\n", now: now,
			fiveHour: ptr(0.05), fiveResets: at(2026, 11, 26, 16, 50)},
		"a time alone, tomorrow": {printed: "Current session: 5% used · resets 1am (Europe/Berlin)\n", now: now,
			fiveHour: ptr(0.05), fiveResets: at(2026, 11, 27, 1, 0)},
		"a reset in the new year": {printed: "Current session: 5% used · resets Jan 1 at 2am (Europe/Berlin)\n",
			now: time.Date(2026, 12, 31, 22, 0, 0, 0, berlin), fiveHour: ptr(0.05), fiveResets: at(2027, 1, 1, 2, 0)},
		"nothing used, no reset": {printed: "Current session: 0% used\nCurrent week (all models): 14% used · resets Dec 2 at 4am (Europe/Berlin)\n",
			now: now, sevenDay: ptr(0.14)},
	} {
		windows, err := usageWindows([]byte(test.printed), test.now)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := windows.FiveHour; (got == nil) != (test.fiveHour == nil) || (got != nil && (got.Utilization != *test.fiveHour || got.ResetsAt != test.fiveResets)) {
			t.Errorf("%s: five_hour = %+v, want %v resetting at %v", name, got, deref(test.fiveHour), time.Unix(test.fiveResets, 0).In(berlin))
		}
		if got := windows.SevenDay; (got == nil) != (test.sevenDay == nil) || (got != nil && got.Utilization != *test.sevenDay) {
			t.Errorf("%s: seven_day = %+v, want %v", name, got, deref(test.sevenDay))
		}
	}

	for name, printed := range map[string]string{
		"no window":      "Usage is not available for this account\n",
		"unknown zone":   "Current session: 5% used · resets Nov 26 at 4:50pm (Mars/Olympus)\n",
		"unreadable day": "Current session: 5% used · resets Thursday at 4:50pm (Europe/Berlin)\n",
	} {
		if _, err := usageWindows([]byte(printed), now); err == nil {
			t.Errorf("%s: read without an error", name)
		}
	}
}

func ptr(value float64) *float64 { return &value }

func deref(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
