package surface

import "fmt"

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
	// runs on the setup-token saved in Settings.
	HostLogin bool `json:"host_login"`
}

// Text is the notice as the owner reads it, the same on every surface.
// "Spool:" says who is speaking: the hub, not the loop whose bot carries it.
func (notice LoginNotice) Text() string {
	if !notice.Refused {
		return "Spool: the Claude login works again, and the loops it stopped are resuming."
	}
	fix := "Replace the setup-token in Spool's Settings."
	if notice.HostLogin {
		fix = "Log in again by running claude on the host. Every bare loop on this host shares that login, so they have all stopped."
	}
	return fmt.Sprintf("Spool: %s has stopped because the Claude login was refused (%s). %s "+
		"The loops retry on their own and resume once the login works; you will hear once more when it does.",
		notice.LoopName, notice.Sentence, fix)
}
