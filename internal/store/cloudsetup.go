package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// CloudSetup is what a cloud loop's sessions start from (ADR-0061): its
// GitHub repo and branch, the further repos the session attaches, and the
// claude.ai environment it runs in.
type CloudSetup struct {
	Repo          string
	Branch        string
	Repos         []string
	EnvironmentID string
}

// DefaultCloudBranch is a cloud loop's branch until the operator names
// another. The hub can't read a private repo's default branch, and the
// start names one (ADR-0061).
const DefaultCloudBranch = "main"

// MaxCloudRepos caps the further repos a cloud loop names.
const MaxCloudRepos = 10

// CloudSetupOf is the loop's setup as its record holds it.
func CloudSetupOf(loopRecord *Loop) CloudSetup {
	return CloudSetup{
		Repo:          loopRecord.CloudRepo,
		Branch:        loopRecord.CloudBranch,
		Repos:         loopRecord.CloudRepos,
		EnvironmentID: loopRecord.CloudEnvironmentID,
	}
}

// Complete reports whether a session can start from setup: a repo, a
// branch and an environment. A loop made under ADR-0059 has none of them
// until the operator sets them.
func (setup CloudSetup) Complete() bool {
	return setup.Repo != "" && setup.Branch != "" && setup.EnvironmentID != ""
}

// Check is ADR-0061's rule for each field, run when they are set and
// again at each start: every repo is owner/name in the characters GitHub
// allows, the branch is a branch name git accepts and doesn't start with
// "-", the environment ID has an environment ID's shape, and no repo is
// named twice. An empty setup passes: it is a loop with none yet.
func (setup CloudSetup) Check() error {
	if setup.Repo == "" && setup.Branch == "" && len(setup.Repos) == 0 && setup.EnvironmentID == "" {
		return nil
	}
	if !ValidGitHubRepo(setup.Repo) {
		return fmt.Errorf("the repo must be a GitHub repo as owner/name, not %q", setup.Repo)
	}
	if !ValidCloudBranch(setup.Branch) {
		return fmt.Errorf("%q is not a branch name git accepts", setup.Branch)
	}
	if !ValidCloudEnvironmentID(setup.EnvironmentID) {
		return errors.New("the environment must be a claude.ai environment ID, env_ and letters and digits")
	}
	if len(setup.Repos) > MaxCloudRepos {
		return fmt.Errorf("a cloud loop names at most %d further repos", MaxCloudRepos)
	}
	seen := map[string]bool{strings.ToLower(setup.Repo): true}
	for _, repo := range setup.Repos {
		if !ValidGitHubRepo(repo) {
			return fmt.Errorf("each further repo must be a GitHub repo as owner/name, not %q", repo)
		}
		// GitHub's names are case-insensitive
		if seen[strings.ToLower(repo)] {
			return fmt.Errorf("%s is named twice", repo)
		}
		seen[strings.ToLower(repo)] = true
	}
	return nil
}

// ValidGitHubRepo reports whether repo is owner/name in the characters
// GitHub allows: an owner of letters, digits and inner single hyphens,
// at most 39, and a name of letters, digits, '.', '-' and '_', at most
// 100, that isn't "." or "..". Both go into a git remote and a --ref the
// start runs with.
func ValidGitHubRepo(repo string) bool {
	owner, name, found := strings.Cut(repo, "/")
	if !found || !validGitHubOwner(owner) || name == "" || len(name) > 100 || name == "." || name == ".." {
		return false
	}
	for _, char := range name {
		if !asciiAlnum(char) && char != '.' && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func validGitHubOwner(owner string) bool {
	if owner == "" || len(owner) > 39 || owner[0] == '-' || owner[len(owner)-1] == '-' || strings.Contains(owner, "--") {
		return false
	}
	for _, char := range owner {
		if !asciiAlnum(char) && char != '-' {
			return false
		}
	}
	return true
}

func asciiAlnum(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}

// ValidCloudBranch reports whether branch is a name `git check-ref-format
// --branch` accepts that doesn't start with "-", so it can never be read
// as a flag: no space, control character or any of ~^:?*[\, no "..",
// "@{" or "//", no component that starts with "." or ends with ".lock",
// not starting or ending with "/", not ending with ".", and not "@".
func ValidCloudBranch(branch string) bool {
	if branch == "" || branch == "@" || len(branch) > 255 || branch[0] == '-' ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") {
		return false
	}
	for _, bad := range []string{"..", "@{", "//"} {
		if strings.Contains(branch, bad) {
			return false
		}
	}
	for _, char := range branch {
		if char < 0x20 || char == 0x7f || strings.ContainsRune(" ~^:?*[\\", char) {
			return false
		}
	}
	for _, component := range strings.Split(branch, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

// What a check of a cloud loop's setup found (#1182).
const (
	CloudCheckOK     = "ok"
	CloudCheckFailed = "failed"
)

// CloudCheck is the last check of a cloud loop's setup (ADR-0061): a
// session started in the loop's environment, and whether the hub heard
// from it. A loop whose check hasn't passed for its current setup is held,
// as one with no repo is.
type CloudCheck struct {
	State string `json:"state"`
	// Stage and Detail say where a failed check stopped, and how.
	Stage     string `json:"stage,omitempty"`
	Detail    string `json:"detail,omitempty"`
	CheckedAt int64  `json:"checked_at"`
	// Address is the hub's public address the check ran against: the setup
	// script the environment holds is that address's.
	Address string `json:"address"`
	// For is the setup the check ran on (CloudCheckFor). A check of
	// another, before an edit, a Rotate token or a kill, is no check.
	For string `json:"for"`
	// Account is the ID of the account the loop's Claude login was signed
	// in as when the check ran, "" when the hub didn't know it. Signing
	// the login in again as another account fails a check that passed,
	// and, where this is known, revokes the loop's hub token (#1230).
	Account string `json:"account,omitempty"`
}

// CloudCheckFor names the setup a check of the loop runs on: its repo,
// branch, environment, hub token and Claude login. The further repos are
// left out, as the check starts from the first alone. Only the token's
// hash takes part, and only hashed again. The default login adds nothing,
// so a pass recorded before a loop could name another still stands.
func CloudCheckFor(loopRecord *Loop) string {
	parts := []string{loopRecord.CloudRepo, loopRecord.CloudBranch, loopRecord.CloudEnvironmentID, loopRecord.HubTokenHash}
	if loopRecord.ClaudeLogin != "" {
		parts = append(parts, loopRecord.ClaudeLogin)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// CloudCheckPassed reports whether the loop's last check passed on the
// setup it has now.
func (loopRecord *Loop) CloudCheckPassed() bool {
	check := loopRecord.CloudCheck
	return check != nil && check.State == CloudCheckOK && check.For == CloudCheckFor(loopRecord)
}
