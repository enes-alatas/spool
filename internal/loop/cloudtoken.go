package loop

import (
	"context"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's hub token belongs to its environment, not to one session
// (ADR-0061): the operator sets it as the environment's secret, so it
// outlives every session the loop starts. The hub refuses it while the
// loop is paused, by the loop's status, and takes it again on a resume. A
// kill revokes it for good: the loop holds its work until Rotate token
// mints another, which the operator sets in its place.

// cloudTokenless reports whether the loop is a cloud loop with no hub
// token, one never set up or killed: it holds its work, as its session
// couldn't reach the hub.
func (actor *Actor) cloudTokenless() bool {
	return actor.loop.Runtime == store.RuntimeCloud && actor.loop.HubMCPToken == ""
}

// cloudSetupChanged follows a cloud loop's token being minted or revoked:
// one with none opens the cloud_setup problem, and one given a token
// clears it and wakes with whatever waited meanwhile, or with a tick when
// one was dropped and nothing else waits.
func (actor *Actor) cloudSetupChanged() {
	if actor.loop.Runtime != store.RuntimeCloud {
		return
	}
	if actor.cloudTokenless() {
		actor.openProblem(store.SymptomCloudSetup, now(), 0)
		actor.publishState()
		return
	}
	actor.clearProblem(store.SymptomCloudSetup, store.ClearedByRecovery)
	if actor.holdsWork() {
		return
	}
	actor.drainStoredInbox()
	if actor.cloudTickDropped && len(actor.inbox) == 0 && (actor.state == StateAsleep || actor.state == StateIdle) {
		actor.enqueue(TickEnvelope(time.Now(), &actor.loop))
	}
	actor.cloudTickDropped = false
	actor.publishState()
	actor.pump()
}

// tokenChanged takes the loop's hub token as the store holds it now, after
// Rotate token, and has the redactor learn it.
func (actor *Actor) tokenChanged(ctx context.Context) {
	stored, err := actor.deps.Store.Loops().Get(ctx, actor.loop.ID)
	if err != nil {
		actor.log().Error("read the loop's new hub token", "err", err)
		return
	}
	actor.loop.HubMCPToken = stored.HubMCPToken
	if actor.deps.SecretsChanged != nil {
		actor.deps.SecretsChanged(ctx)
	}
	actor.cloudSetupChanged()
}

// cutOffCloud ends a cloud loop's session at once, as a stop or a kill
// does, and the next wake starts a session of its own. A stop's token is
// refused by the loop's status already; a kill revokes it for good. The
// CLI can't interrupt a cloud session, so a turn in flight runs on in the
// VM, cut off from the hub. The session is ended for endReason. Any other
// loop is left as it is. The error is a revocation that failed: the rest
// is still done, but the session can reach the hub until the loop is
// killed again (#1114).
func (actor *Actor) cutOffCloud(ctx context.Context, verb, endReason string, revoke bool) error {
	if actor.loop.Runtime != store.RuntimeCloud {
		return nil
	}
	var err error
	if revoke {
		if err = actor.deps.Store.Loops().RevokeHubMCPToken(ctx, actor.loop.ID); err != nil {
			actor.log().Error("cloud loop's token not revoked; its session can still reach the hub", "verb", verb, "err", err)
		} else {
			actor.loop.HubMCPToken = ""
			if actor.deps.SecretsChanged != nil {
				actor.deps.SecretsChanged(ctx)
			}
		}
	}
	actor.stopProcess(verb)
	actor.forgetSession(ctx, endReason)
	if revoke && err == nil {
		actor.cloudSetupChanged()
	}
	return err
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
