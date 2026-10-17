package httpapi

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// CloudSessions reads a cloud loop's session on claude.ai (ADR-0056).
type CloudSessions interface {
	// SessionURL is the loop's current session page, "" while it has none.
	SessionURL(loopID string) string
}

// cloudRepoRe is a GitHub repo as a cloud session's source takes it:
// GitHub's own owner and repo name rules, over HTTPS.
var cloudRepoRe = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// cloudRepo reads the repo a cloud loop was asked to start from into the
// form its sessions get, "" for none: a trailing slash or .git, as a
// clone URL has, is dropped.
func cloudRepo(raw string) (string, error) {
	repo := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(raw), "/"), ".git")
	if repo == "" {
		return "", nil
	}
	name := repo[strings.LastIndex(repo, "/")+1:]
	if !cloudRepoRe.MatchString(repo) || name == "." || name == ".." {
		return "", fmt.Errorf("cloud_repo must be a GitHub repo, as https://github.com/<owner>/<repo>")
	}
	return repo, nil
}

// cloudReady is nil when Settings → Cloud is green, as its page shows it:
// the public address's last check passed, and an account is signed in
// whose last check passed. Otherwise it says which step to finish.
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
	return nil
}
