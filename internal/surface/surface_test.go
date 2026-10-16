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
	}
	for _, testCase := range cases {
		if got := testCase.notice.Text(); !strings.HasPrefix(got, testCase.want) {
			t.Errorf("%+v: Text() = %q, want it to open %q", testCase.notice, got, testCase.want)
		}
	}
}
