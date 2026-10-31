package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/runtime/cloud"
	"github.com/enes-alatas/spool/internal/store"
)

// newCloudRuntime is the cloud runtime as this hub wires it (ADR-0059): the
// account is read from the store at each CLI run, so a sign-in takes
// effect at once, and its state lives in the data directory. The CLI's
// runs, start directories among them, are made outside the data directory,
// which backups carry. Each hub's dir is its own, named for its data
// directory, so one hub's boot sweep never removes another's runs.
func newCloudRuntime(st store.Store, dataDir, claudeBin, apiBase string) *cloud.Runtime {
	return &cloud.Runtime{
		CLI: &cloud.CLI{Binary: claudeBin, Dir: cloudCLIDir(dataDir), Env: signInEnv(os.Environ())},
		Account: func(ctx context.Context, login string) (cloud.Account, error) {
			// a cloud loop runs on its own login, "" for the default
			// (ADR-0061, #1078)
			if login == "" {
				login = store.DefaultClaudeLogin
			}
			account, signedIn, err := store.SignedInAccount(ctx, st.ClaudeLogins(), login)
			if err != nil {
				return cloud.Account{}, err
			}
			if !signedIn {
				return cloud.Account{}, cloud.ErrNoAccount
			}
			return cloud.Account{Token: account.Token, OrgUUID: account.OrgUUID}, nil
		},
		APIBase: apiBase,
		Dir:     filepath.Join(dataDir, "cloud"),
	}
}

// cloudCLIDir is where this hub's cloud CLI runs are made: the temp dir,
// under a name of the data directory's own.
func cloudCLIDir(dataDir string) string {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join(os.TempDir(), "spool-cloud-"+hex.EncodeToString(sum[:6]))
}

// anthropicAPI is Anthropic's API, the Claude proxy's upstream.
const anthropicAPI = "https://api.anthropic.com"

// claudeAPIBase checks --cloud-api-base: the Claude proxy's calls carry the
// hub's Claude token, so the API is reached over HTTPS, or over plain HTTP
// to a stand-in on this machine's loopback, which is what tests point it
// at.
func claudeAPIBase(raw string) (string, error) {
	return raw, httpapi.HTTPSOrLoopback(raw)
}

// cloudAddress is the public address cloud sessions reach the hub at, ""
// while the operator has set none. It is checked at every use, whatever
// wrote it: the loop's token is sent to it, so an address it would cross
// in the clear, or that no cloud session could reach, is refused, and no
// cloud loop wakes on it. loopback is --cloud-loopback-address, which
// only tests set (httpapi.CloudAddressAllowed).
func cloudAddress(ctx context.Context, settings store.SettingsStore, loopback bool) (string, error) {
	address, err := settings.Get(ctx, store.SettingCloudAddress)
	switch {
	case errors.Is(err, store.ErrNotFound), err == nil && address == "":
		return "", nil
	case err != nil:
		return "", err
	}
	if err := httpapi.CloudAddressAllowed(address, loopback); err != nil {
		return "", fmt.Errorf("the cloud address: %w", err)
	}
	return strings.TrimSuffix(address, "/"), nil
}

// cloudAccounts makes the account check (ADR-0059): ADR-0044's login check
// on the account's token. The host's claude runs it when the hub finds
// one, as it runs cloud sessions, or else the workstation image's does, as
// it runs the sign-in on a hub that has only the image (#952).
type cloudAccounts struct {
	runtime *cloud.Runtime
	image   interface {
		CheckLogin(ctx context.Context, token string) (claude.LoginCheck, error)
	}
}

// CheckAccount makes the check, and names where a failed one stopped as
// Settings → Cloud does.
func (accounts cloudAccounts) CheckAccount(ctx context.Context) (stage, detail string) {
	check, err := accounts.check(ctx)
	switch {
	case errors.Is(err, cloud.ErrNoAccount):
		return httpapi.CloudAccountStageNone, err.Error()
	case err != nil:
		return httpapi.CloudAccountStageOther, err.Error()
	case !check.OK:
		return httpapi.CloudAccountStageRefused, check.Refusal
	}
	return "", ""
}

func (accounts cloudAccounts) check(ctx context.Context) (claude.LoginCheck, error) {
	if _, err := exec.LookPath(accounts.runtime.CLI.Binary); err == nil || accounts.image == nil {
		return accounts.runtime.CheckAccount(ctx)
	}
	account, err := accounts.runtime.Account(ctx, "")
	if err != nil {
		return claude.LoginCheck{}, err
	}
	return accounts.image.CheckLogin(ctx, account.Token)
}

// storedSetting reads the setting key, "" while it is unset.
func storedSetting(settings store.SettingsStore, key string) (string, error) {
	value, err := settings.Get(context.Background(), key)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return value, err
}

// cloudEnvironments is a Claude login's list for the environment pick.
type cloudEnvironments struct{ runtime *cloud.Runtime }

func (environments cloudEnvironments) Environments(ctx context.Context, login string) ([]httpapi.CloudEnvironment, error) {
	listed, err := environments.runtime.Environments(ctx, login)
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.CloudEnvironment, 0, len(listed))
	for _, environment := range listed {
		out = append(out, httpapi.CloudEnvironment{ID: environment.ID, Name: environment.Name})
	}
	return out, nil
}

// cloudSessions is each cloud loop's session link and its turn that may be
// running, for the loop's page.
type cloudSessions struct{ runtime *cloud.Runtime }

func (sessions cloudSessions) SessionURL(loopID string) string {
	return sessions.runtime.SessionURL(loopID)
}

func (sessions cloudSessions) Turn(loopID string) *httpapi.CloudTurn {
	turn, ok := sessions.runtime.Turn(loopID)
	if !ok {
		return nil
	}
	return &httpapi.CloudTurn{Since: turn.Since, Until: turn.Until, SessionURL: turn.SessionURL, CutOff: turn.CutOff}
}
