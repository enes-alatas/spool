package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
func newCloudRuntime(settings store.SettingsStore, dataDir, apiBase string, loopbackAddress bool) *cloud.Runtime {
	return &cloud.Runtime{
		Client: &cloud.Client{
			BaseURL: apiBase,
			Account: func(ctx context.Context) (cloud.Account, error) {
				raw, err := storedSetting(settings, store.SettingCloudAccount)
				if err != nil {
					return cloud.Account{}, err
				}
				if raw == "" {
					return cloud.Account{}, cloud.ErrNoAccount
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
			return cloudEgressEntries(ctx, settings, loopbackAddress)
		},
	}
}

// cloudAPIBase checks --cloud-api-base: the calls carry the account's
// token, so the API is reached over HTTPS, or over plain HTTP to a stand-in
// on this machine's loopback, which is what tests point it at.
func cloudAPIBase(raw string) (string, error) {
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

// cloudEgressEntries are the egress allowlist entries every cloud
// environment is held to: the hub's egress allowlist, built-in and the
// operator's, and the public address's host. The runtime translates them,
// leaving off what has no form in a cloud environment (cloud.Allowlist).
func cloudEgressEntries(ctx context.Context, settings store.SettingsStore, loopbackAddress bool) ([]string, error) {
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
	address, err := cloudAddress(ctx, settings, loopbackAddress)
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

// CheckAccount makes the account check (ADR-0056), and names where a
// failed one stopped as Settings → Cloud does.
func (environments cloudEnvironments) CheckAccount(ctx context.Context) (stage, detail string) {
	err := environments.runtime.Client.CheckAccount(ctx)
	var apiErr *cloud.APIError
	switch {
	case err == nil:
		return "", ""
	case errors.Is(err, cloud.ErrNoAccount):
		return httpapi.CloudAccountStageNone, err.Error()
	case errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden):
		return httpapi.CloudAccountStageRefused, err.Error()
	case errors.As(err, &apiErr):
		return httpapi.CloudAccountStageOther, err.Error()
	}
	return httpapi.CloudAccountStageUnreachable, err.Error()
}

// storedSetting reads the setting key, "" while it is unset.
func storedSetting(settings store.SettingsStore, key string) (string, error) {
	value, err := settings.Get(context.Background(), key)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return value, err
}
