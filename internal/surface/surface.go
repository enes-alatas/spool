// Package surface defines the Surface seam (ADR-0004, ADR-0029): the chat
// platform a loop meets humans on. Implementations live in subpackages —
// telegram today, slack at L3 — and the hub talks only to this interface.
//
// The seam is narrow because most of a surface's work does not cross it. A
// surface reaches the hub the way any caller does, and the hub reaches back
// the same way:
//
//   - inbound, a surface hands a received message to the router, which owns
//     mentions, the storm guard and delivery (ADR-0002: transport is the
//     surface's, delivery is the hub's);
//   - outbound, a surface subscribes to the bus and mirrors what it is
//     addressed to (ADR-0025), minting the platform ids the hub later quotes
//     as reply references (ADR-0026).
//
// What is left is what the hub must *ask* of a surface, and that is this
// interface: start it, check a credential before storing it, and tell it when
// a loop's configuration changed or the loop went away.
//
// Unlike the SandboxRuntime seam, a surface is a hub adapter: it lives in the
// hub process and reads hub state. The interface stays free of store rows all
// the same (the arch test enforces it) — a loop crosses as its id, and the
// implementation reads the row it needs. That is also why LoopChanged says
// only that something changed: which fields a surface cares about is the
// surface's business, not the API handler's.
package surface

import (
	"context"
	"errors"
)

// Credential is what the operator pastes to give a loop its identity on a
// surface. Token is the bot's own credential on every surface: Telegram's
// bot token, Slack's bot token. AppToken is Slack's app-level token, which
// opens a Socket Mode connection; "" on a surface that has no such thing.
type Credential struct {
	Token    string
	AppToken string
}

// Identity is what the platform says a credential names. Name is the bot's
// handle (a Telegram bot username, a Slack bot name). The other fields are
// Slack's: the bot's user id, which a mention names it by, and the
// workspace it belongs to. They are "" on Telegram.
type Identity struct {
	Name     string
	UserID   string
	TeamID   string
	TeamName string
}

// RejectedError is a credential the platform refused, saying which part: the
// operator pasted two tokens on Slack, and the form has to say which one to
// fix. Err is the platform's own reason.
type RejectedError struct {
	Part string // PartToken or PartAppToken
	Err  error
}

const (
	PartToken    = "token"
	PartAppToken = "app_token"
)

// Error is the platform's reason alone: the part refused is for the caller
// to key on, and every caller already says what it was validating.
func (e *RejectedError) Error() string { return e.Err.Error() }
func (e *RejectedError) Unwrap() error { return e.Err }

// RejectedPart reports which part of a credential err refused, or "" when err
// is not a refusal: a platform that could not be reached has not judged the
// credential at all.
func RejectedPart(err error) string {
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		return rejected.Part
	}
	return ""
}

// Surface is one chat platform, serving every loop that has an identity on
// it. Implementations are goroutine-safe: the API calls them from request
// handlers while their own pollers run.
type Surface interface {
	// Start brings up the surface for every loop already configured, and
	// runs until ctx is cancelled.
	Start(ctx context.Context)

	// ValidateCredential checks a loop credential against the platform and
	// returns the identity it names: a bot username on Telegram; a bot user,
	// its name and its workspace on Slack. It is called before the
	// credential is stored, so a rejected one never reaches the row. A
	// refusal is a *RejectedError; any other error means the platform gave
	// no answer.
	ValidateCredential(ctx context.Context, credential Credential) (Identity, error)

	// LoopChanged tells the surface that a loop's stored configuration has
	// changed, and that it should re-read it. The write has already landed
	// when this is called. It is called for every edit, whatever was
	// edited: deciding that a change was none of a surface's business is
	// the surface's decision to make, not the caller's.
	LoopChanged(ctx context.Context, loopID string)

	// LoopRemoved tells the surface a loop is gone and any presence it has
	// on the platform should stop.
	LoopRemoved(loopID string)

	// Status describes what the surface is doing for one loop, for the
	// control room to render. The shape is the implementation's own.
	Status(loopID string) any
}
