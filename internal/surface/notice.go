package surface

import (
	"fmt"
	"time"
)

// LoginNotice is the payload of bus.KindClaudeLogin: a loop's Claude login
// was refused, or a turn ran on it again after a refusal (#419). It is a hub
// notice, which the hub tells a loop's owner in its own voice because the
// loop cannot: a refused login leaves it no turn to say it with (ADR-0029).
type LoginNotice struct {
	LoopID   string `json:"loop_id"`
	LoopName string `json:"loop_name"`
	// Refused is true when the login was refused, false when a turn ran
	// on it again.
	Refused bool `json:"refused"`
	// Sentence is the CLI's own words for the refusal; "" when Refused is
	// false.
	Sentence string `json:"sentence,omitempty"`
	// HostLogin is true for a loop that runs on the host's own claude login
	// (bare), which every bare loop on the host shares; false for one that
	// runs on the hub's Claude token: its Claude login, or a setup-token.
	HostLogin bool `json:"host_login"`
}

// Text is the notice as the owner reads it, the same on every surface.
// "Spool:" says who is speaking: the hub, not the loop whose bot carries it.
func (notice LoginNotice) Text() string {
	if !notice.Refused {
		return "Spool: the Claude login works again, and the loops it stopped are resuming."
	}
	fix := "Sign in with Claude again in Spool's Settings."
	if notice.HostLogin {
		fix = "Log in again by running claude on the host. Every bare loop on this host shares that login, so they have all stopped."
	}
	return fmt.Sprintf("Spool: %s has stopped because the Claude login was refused (%s). %s "+
		"The loops retry on their own and resume once the login works; you will hear once more when it does.",
		notice.LoopName, notice.Sentence, fix)
}

// ProblemNotice is the payload of bus.KindLoopProblem: the hub's health
// check opened a problem with a loop, or the problem cleared (ADR-0053,
// #764). It is a hub notice, told to the loop's owner once when it opens
// and once when it clears, because a loop that is down or failing cannot
// be relied on to say so itself (ADR-0029).
type ProblemNotice struct {
	LoopID   string `json:"loop_id"`
	LoopName string `json:"loop_name"`
	// Symptom is the store's: "workstation_down", "turn_errors" or
	// "cloud_address".
	Symptom string `json:"symptom"`
	// Opened is true when the problem opened, false when it cleared.
	Opened bool `json:"opened"`
	// Since is when the problem opened, in Unix ms.
	Since int64 `json:"since"`
	// Turns is how many errored turns in a row opened a turn_errors
	// problem.
	Turns int `json:"turns,omitempty"`
	// ByOperator is true when the operator cleared the problem by acting on
	// the loop, false when the signal itself cleared.
	ByOperator bool `json:"by_operator,omitempty"`
}

// Text is the notice as the owner reads it, the same on every surface.
// The hub knows no address a phone could open the control room at, so the
// notice names the loop rather than linking to it.
func (notice ProblemNotice) Text() string {
	since := time.UnixMilli(notice.Since).UTC().Format("15:04 UTC")
	if notice.Opened {
		switch notice.Symptom {
		case "workstation_down":
			return fmt.Sprintf("Spool: %s's workstation has been down since %s, so the loop is not running. "+
				"The hub keeps retrying, and the loop's page in the control room says why it is down. "+
				"You will hear once more when this clears.", notice.LoopName, since)
		case "cloud_address":
			return fmt.Sprintf("Spool: the hub's public address has failed its check since %s, so the cloud loop %s is not running. "+
				"Its messages wait until the address passes. Settings → Cloud in the control room says what failed. "+
				"You will hear once more when this clears.", since, notice.LoopName)
		}
		return fmt.Sprintf("Spool: %s's last %d turns all ended in an error, the first at %s. "+
			"Its timeline in the control room shows what failed. You will hear once more when this clears.",
			notice.LoopName, notice.Turns, since)
	}
	if notice.ByOperator {
		problem := notice.LoopName + "'s errored turns"
		switch notice.Symptom {
		case "workstation_down":
			problem = notice.LoopName + "'s workstation being down"
		case "cloud_address":
			problem = "the public address holding " + notice.LoopName
		}
		return fmt.Sprintf("Spool: the alert about %s was cleared in the control room. "+
			"You will hear again if it comes back.", problem)
	}
	switch notice.Symptom {
	case "workstation_down":
		return fmt.Sprintf("Spool: %s's workstation is up again.", notice.LoopName)
	case "cloud_address":
		return fmt.Sprintf("Spool: the hub's public address passes its check again, so the cloud loop %s is running.", notice.LoopName)
	}
	return fmt.Sprintf("Spool: %s finished a turn without an error again.", notice.LoopName)
}
