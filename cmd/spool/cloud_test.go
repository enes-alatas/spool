package main

import (
	"context"
	"slices"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

type mapSettings struct {
	store.SettingsStore
	values map[string]string
}

func (settings mapSettings) Get(_ context.Context, key string) (string, error) {
	if value, ok := settings.values[key]; ok {
		return value, nil
	}
	return "", store.ErrNotFound
}

// The API's calls carry the account's token, so plain HTTP is taken only
// to a stand-in on loopback.
func TestCloudAPIBase(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.anthropic.com": true,
		"http://127.0.0.1:4000":     true,
		"http://[::1]:4000":         true,
		"http://api.anthropic.com":  false,
		"http://0.0.0.0:4000":       false,
		"http://10.0.0.1:4000":      false,
		"ftp://127.0.0.1":           false,
		"api.anthropic.com":         false,
	} {
		if _, err := cloudAPIBase(raw); (err == nil) != ok {
			t.Errorf("cloudAPIBase(%q) = %v, want ok %v", raw, err, ok)
		}
	}
}

// A cloud environment's allowlist is the hub's egress allowlist and the
// public host, less every entry that would not translate: never wider.
func TestCloudAllowedHosts(t *testing.T) {
	settings := mapSettings{values: map[string]string{
		store.SettingEgressHosts:  `{"hosts":["pypi.org","registry.example.test:8443",".example.test","github.com"]}`,
		store.SettingCloudAddress: "https://hub.example.test/",
	}}
	hosts, err := cloudAllowedHosts(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api.anthropic.com", "pypi.org", "hub.example.test"} {
		if !slices.Contains(hosts, want) {
			t.Errorf("%s is missing from %v", want, hosts)
		}
	}
	for _, refused := range []string{"registry.example.test:8443", ".example.test", ".githubusercontent.com"} {
		if slices.Contains(hosts, refused) {
			t.Errorf("%s doesn't translate and was kept", refused)
		}
	}
	seen := map[string]bool{}
	for _, host := range hosts {
		if seen[host] {
			t.Errorf("%s is listed twice", host)
		}
		seen[host] = true
	}
}

// The loop's token is sent to the cloud address, so one it would cross in
// the clear is refused at use, whatever wrote it, and so is every allowlist
// built from it.
func TestCloudAddressFailsClosed(t *testing.T) {
	for stored, ok := range map[string]bool{
		"https://hub.example.test/": true,
		"http://127.0.0.1:9000":     true,
		"http://hub.example.test":   false,
		"hub.example.test":          false,
	} {
		settings := mapSettings{values: map[string]string{store.SettingCloudAddress: stored}}
		address, err := cloudAddress(context.Background(), settings)
		if (err == nil) != ok || (!ok && address != "") {
			t.Errorf("cloudAddress(%q) = %q, %v; want ok %v", stored, address, err, ok)
		}
		if _, err := cloudAllowedHosts(context.Background(), settings); (err == nil) != ok {
			t.Errorf("cloudAllowedHosts with %q: %v, want ok %v", stored, err, ok)
		}
	}
	if address, err := cloudAddress(context.Background(), mapSettings{}); address != "" || err != nil {
		t.Errorf("no address set: %q, %v", address, err)
	}
}
