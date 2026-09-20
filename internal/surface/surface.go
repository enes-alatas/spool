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

import "context"

// Surface is one chat platform, serving every loop that has an identity on
// it. Implementations are goroutine-safe: the API calls them from request
// handlers while their own pollers run.
type Surface interface {
	// Start brings up the surface for every loop already configured, and
	// runs until ctx is cancelled.
	Start(ctx context.Context)

	// ValidateCredential checks a loop credential against the platform and
	// returns the identity it names — a bot username on Telegram. It is
	// called before the credential is stored, so a rejected one never
	// reaches the row.
	ValidateCredential(ctx context.Context, credential string) (identity string, err error)

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
