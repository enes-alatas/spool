package loop

import (
	"context"

	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's hub token is in its VM for the session's life, and in the
// bundle the session started from (ADR-0059). So each session gets a token
// of its own, and a stop or a kill revokes it at once: a copy lifted out of
// a session dies with that session, and an old VM of the loop can't bind
// itself to a start of a new one.

// mintHubToken gives the loop a fresh hub token, which revokes the one it
// held, and has the redactor learn it before any session holds it.
func (actor *Actor) mintHubToken(ctx context.Context) error {
	token, err := actor.deps.Store.Loops().RotateHubMCPToken(ctx, actor.loop.ID)
	if err != nil {
		return err
	}
	actor.loop.HubMCPToken = token
	if actor.deps.SecretsChanged != nil {
		actor.deps.SecretsChanged(ctx)
	}
	return nil
}

// cutOffCloud ends a cloud loop's session at once, as a stop or a kill
// does: its token is revoked, and the next wake starts a session of its
// own, on a token of its own. The CLI can't interrupt a cloud session, so
// a turn in flight runs on in the VM, cut off from the hub. The session is
// ended for endReason. Any other loop is left as it is.
func (actor *Actor) cutOffCloud(ctx context.Context, verb, endReason string) {
	if actor.loop.Runtime != store.RuntimeCloud {
		return
	}
	if err := actor.mintHubToken(ctx); err != nil {
		actor.log().Error("cloud loop's token not revoked; its session can still reach the hub", "verb", verb, "err", err)
	}
	actor.stopProcess(verb)
	actor.forgetSession(ctx, endReason)
}

// cloudHandOff opens the cloud_connections problem when the loop's session
// was refused its connections, since its shell may now run without them,
// and clears it when a session is handed them again (ADR-0059).
func (actor *Actor) cloudHandOff(answered bool) {
	if actor.loop.Runtime != store.RuntimeCloud {
		return
	}
	if answered {
		actor.clearProblem(store.SymptomCloudConnections, store.ClearedByRecovery)
		return
	}
	actor.openProblem(store.SymptomCloudConnections, now(), 0)
}
