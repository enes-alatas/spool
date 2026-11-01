package surface

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A refusal says which token to fix, through any wrapping; an error that is
// not a refusal says nothing about the credential (#230).
func TestRejectedPart(t *testing.T) {
	reason := errors.New("invalid_auth")
	refused := fmt.Errorf("validating: %w", &RejectedError{Part: PartAppToken, Err: reason})
	if got := RejectedPart(refused); got != PartAppToken {
		t.Errorf("RejectedPart(wrapped refusal) = %q, want %q", got, PartAppToken)
	}
	if !errors.Is(refused, reason) {
		t.Error("a refusal does not unwrap to the platform's reason")
	}
	if got := refused.Error(); got != "validating: invalid_auth" {
		t.Errorf("message = %q: the part is for the caller to key on, not to print", got)
	}
	if got := RejectedPart(errors.New("dial tcp: connection refused")); got != "" {
		t.Errorf("RejectedPart(unreachable) = %q, want \"\"", got)
	}
}

// A problem notice names the loop and the symptom, and says who cleared it
// (ADR-0053).
func TestProblemNoticeText(t *testing.T) {
	since := time.Date(2026, 10, 14, 9, 12, 0, 0, time.UTC).UnixMilli()
	cases := []struct {
		notice ProblemNotice
		want   string
	}{
		{ProblemNotice{LoopName: "gardener", Symptom: "turn_errors", Opened: true, Since: since, Turns: 3},
			"Spool: gardener's last 3 turns all ended in an error, the first at 09:12 UTC."},
		{ProblemNotice{LoopName: "gardener", Symptom: "workstation_down", Opened: true, Since: since},
			"Spool: gardener's workstation has been down since 09:12 UTC, so the loop is not running."},
		{ProblemNotice{LoopName: "gardener", Symptom: "turn_errors"},
			"Spool: gardener finished a turn without an error again."},
		{ProblemNotice{LoopName: "gardener", Symptom: "workstation_down"},
			"Spool: gardener's workstation is up again."},
		{ProblemNotice{LoopName: "gardener", Symptom: "workstation_down", ByOperator: true},
			"Spool: the alert about gardener's workstation being down was cleared in the control room."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_address", Opened: true, Since: since},
			"Spool: the hub's public address has failed its check since 09:12 UTC, so the cloud loop cirrus is not running."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_address"},
			"Spool: the hub's public address passes its check again, so the cloud loop cirrus is running."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_address", ByOperator: true},
			"Spool: the alert about the public address holding cirrus was cleared in the control room."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_connections", Opened: true, Since: since},
			"Spool: the cloud loop cirrus's session asked for its connections at 09:12 UTC and the hub refused, so its shell commands may run without them."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_connections"},
			"Spool: the cloud loop cirrus's session was handed its connections again."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_connections", ByOperator: true},
			"Spool: the alert about cirrus's session being refused its connections was cleared in the control room."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_setup", Opened: true, Since: since},
			"Spool: the cloud loop cirrus's setup has been unfinished since 09:12 UTC, so it is not running."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_setup"},
			"Spool: the cloud loop cirrus's setup is done, so it is running."},
		{ProblemNotice{LoopName: "cirrus", Symptom: "cloud_setup", ByOperator: true},
			"Spool: the alert about cirrus's unfinished setup was cleared in the control room."},
		{ProblemNotice{LoopName: "gardener", Symptom: "turn_stuck", Opened: true, Since: since, Minutes: 120},
			"Spool: gardener's turn has been running since 09:12 UTC, 120 minutes so far, and is still going."},
		{ProblemNotice{LoopName: "gardener", Symptom: "turn_stuck"},
			"Spool: gardener's long turn has ended."},
		{ProblemNotice{LoopName: "gardener", Symptom: "turn_stuck", ByOperator: true},
			"Spool: the alert about gardener's long turn was cleared in the control room."},
	}
	for _, testCase := range cases {
		if got := testCase.notice.Text(); !strings.HasPrefix(got, testCase.want) {
			t.Errorf("%+v: Text() = %q, want it to open %q", testCase.notice, got, testCase.want)
		}
	}
	// A loop held only for its setup check, as every cloud loop is after
	// the upgrade (#1182), is told of the check and where it runs.
	held := ProblemNotice{LoopName: "cirrus", Symptom: "cloud_setup", Opened: true, Since: since}
	if got := held.Text(); !strings.Contains(got, "passed a setup check") || !strings.Contains(got, "setup view") {
		t.Errorf("cloud_setup notice = %q, want it to name the setup check and the setup view", got)
	}
}
