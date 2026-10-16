package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/runtime/cloud"
	"github.com/enes-alatas/spool/internal/store"
)

// newCloudRuntime is the cloud runtime as this hub wires it (ADR-0056): the
// account and the public address are read from the store at each call, so
// a sign-in or a new address takes effect at once, and its state lives in
// the data directory.
func newCloudRuntime(settings store.SettingsStore, dataDir, apiBase string) *cloud.Runtime {
	return &cloud.Runtime{
		Client: &cloud.Client{
			BaseURL: apiBase,
			Account: func(ctx context.Context) (cloud.Account, error) {
				raw, err := settings.Get(ctx, store.SettingCloudAccount)
				if errors.Is(err, store.ErrNotFound) {
					return cloud.Account{}, cloud.ErrNoAccount
				}
				if err != nil {
					return cloud.Account{}, err
				}
				var account store.CloudAccount
				if err := json.Unmarshal([]byte(raw), &account); err != nil || account.Token == "" {
					return cloud.Account{}, errors.New("cloud: the stored Claude account can't be read; sign in again")
				}
				return cloud.Account{Token: account.Token, OrgUUID: account.OrgUUID}, nil
			},
		},
		Dir: filepath.Join(dataDir, "cloud"),
		EgressEntries: func(ctx context.Context) ([]string, error) {
			return cloudEgressEntries(ctx, settings)
		},
	}
}

// cloudAPIBase checks --cloud-api-base: the calls carry the account's
// token, so the API is reached over HTTPS, or over plain HTTP to a stand-in
// on this machine's loopback, which is what tests point it at.
func cloudAPIBase(raw string) (string, error) {
	return raw, httpsOrLoopback(raw)
}

// httpsOrLoopback refuses a URL a credential would cross in the clear: one
// that is neither HTTPS nor plain HTTP to this machine's loopback.
func httpsOrLoopback(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%q is not a URL", raw)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("%q must be https, or http on a loopback address", raw)
}

// cloudAddress is the public address cloud sessions reach the hub at, ""
// while the operator has set none. It is checked at every use, whatever
// wrote it: the loop's token is sent to it, so an address it would cross
// in the clear is refused, and no cloud loop wakes on it.
func cloudAddress(ctx context.Context, settings store.SettingsStore) (string, error) {
	address, err := settings.Get(ctx, store.SettingCloudAddress)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", nil
	case err != nil:
		return "", err
	}
	if err := httpsOrLoopback(address); err != nil {
		return "", fmt.Errorf("the cloud address: %w", err)
	}
	return strings.TrimSuffix(address, "/"), nil
}

// cloudEgressEntries are the egress allowlist entries every cloud
// environment is held to: the hub's egress allowlist, built-in and the
// operator's, and the public address's host. The runtime translates them,
// leaving off what has no form in a cloud environment (cloud.Allowlist).
func cloudEgressEntries(ctx context.Context, settings store.SettingsStore) ([]string, error) {
	entries := egress.DefaultHosts()
	raw, err := settings.Get(ctx, store.SettingEgressHosts)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		var stored store.EgressHosts
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return nil, err
		}
		entries = append(entries, stored.Hosts...)
	}
	address, err := cloudAddress(ctx, settings)
	if err != nil {
		return nil, err
	}
	if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" {
		entries = append(entries, parsed.Hostname())
	}
	return entries, nil
}

// cloudEnvironments is the cloud runtime as the egress routes drive it.
// httpapi may not import a runtime adapter (archtest), so it names an
// interface of its own, and this fits the runtime to it.
type cloudEnvironments struct{ runtime *cloud.Runtime }

func (environments cloudEnvironments) SyncEnvironments(ctx context.Context) error {
	return environments.runtime.Sync(ctx)
}

func (environments cloudEnvironments) EnvironmentSyncs(ctx context.Context) ([]httpapi.CloudEnvironmentSync, []string, error) {
	syncs, notApplied, err := environments.runtime.Syncs(ctx)
	if err != nil {
		return nil, nil, err
	}
	var converted []httpapi.CloudEnvironmentSync
	for _, sync := range syncs {
		converted = append(converted, httpapi.CloudEnvironmentSync(sync))
	}
	return converted, notApplied, nil
}
