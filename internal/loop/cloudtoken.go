package loop

import (
	"context"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's sessions start from its repo, in its environment, on its
// hub token (ADR-0061). The token belongs to the environment, not to one
// session: the operator sets it as the environment's secret, so it
// outlives every session the loop starts, and the hub keeps only its
// hash. The hub refuses it while the loop is paused, by the loop's status,
// and takes it again on a resume. A kill, or a move to another Claude
// login, whose account's environment needs a token of its own (#1078),
// revokes it for good: the loop
// holds its work until Rotate token mints another, which the operator sets
// in its place.

// cloudUnset reports whether the loop is a cloud loop whose setup isn't
// done: it names no repo, branch or environment, holds no hub token, one
// never minted or killed, or hasn't passed its setup check on the setup it
// has now (#1182). It holds its work, as no session of it could start or
// reach the hub, or none is known to.
func (actor *Actor) cloudUnset() bool {
	return actor.loop.Runtime == store.RuntimeCloud && (!actor.loop.HasHubToken ||
		actor.loop.CloudRepo == "" || actor.loop.CloudBranch == "" || actor.loop.CloudEnvironmentID == "" ||
		!actor.cloudChecked())
}

// cloudChecked reports whether the loop's setup check passed on the setup
// it has now, against the public address that last passed its own check:
// the environment's setup script names the address it was pasted for, so
// a check against another stands again only once that address is back.
// While no address passes, the cloud_address problem holds the loop, and
// the address takes no part here.
func (actor *Actor) cloudChecked() bool {
	if !actor.loop.CloudCheckPassed() {
		return false
	}
	gate := actor.deps.CloudGate
	if gate == nil {
		return true
	}
	passed := gate.PassedAddress()
	return passed == "" || passed == actor.loop.CloudCheck.Address
}

// cloudSetupChanged follows a cloud loop's setup changing, its token
// minted or revoked or its repo or environment edited: one whose setup
// isn't done opens the cloud_setup problem, and one whose setup is done
// clears it and wakes with whatever waited meanwhile, or with a tick when
// one was dropped and nothing else waits.
func (actor *Actor) cloudSetupChanged() {
	if actor.loop.Runtime != store.RuntimeCloud {
		return
	}
	if actor.cloudUnset() {
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

// tokenChanged takes whether the loop holds a hub token as the store says
// now, after Rotate token or a move to another Claude login.
func (actor *Actor) tokenChanged(ctx context.Context) {
	stored, err := actor.deps.Store.Loops().Get(ctx, actor.loop.ID)
	if err != nil {
		actor.log().Error("read the loop's new hub token", "err", err)
		return
	}
	actor.loop.HasHubToken, actor.loop.HubTokenHash = stored.HasHubToken, stored.HubTokenHash
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
			actor.loop.HasHubToken, actor.loop.HubTokenHash = false, ""
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
