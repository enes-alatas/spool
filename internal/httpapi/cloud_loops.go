package httpapi

import (
	"context"
	"errors"
)

// CloudSessions reads a cloud loop's session on claude.ai (ADR-0056).
type CloudSessions interface {
	// SessionURL is the loop's current session page, "" while it has none.
	SessionURL(loopID string) string
}

// cloudReady is nil when Settings → Cloud is green, as its page shows it:
// the public address's last check passed, an account is signed in whose
// last check passed, and an environment is chosen. Otherwise it says which
// step to finish. An allowlist not confirmed is a warning on the loops, not
// a hold (ADR-0059).
func (server *Server) cloudReady(ctx context.Context) error {
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		return err
	}
	if address == "" {
		return errors.New("set the hub's public address in Settings → Cloud first")
	}
	if check := server.cloudCheck.result(address); check == nil || check.State != CloudCheckOK {
		return errors.New("the public address in Settings → Cloud hasn't passed its check")
	}
	set, err := server.cloudAccountSet(ctx)
	if err != nil {
		return err
	}
	if !set {
		return errors.New("sign in to a Claude account in Settings → Cloud first")
	}
	if check := server.cloudAccountCheck.result(); check == nil || check.State != CloudCheckOK {
		return errors.New("the Claude account in Settings → Cloud hasn't passed its check")
	}
	chosen, err := server.storedCloudEnvironment(ctx)
	if err != nil {
		return err
	}
	if chosen.ID == "" {
		return errors.New("choose the environment cloud loops start in, in Settings → Cloud, first")
	}
	return nil
}
