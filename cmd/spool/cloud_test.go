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

// A cloud environment is held to the hub's egress allowlist, built-in and
// stored, and the public host. The runtime translates them (cloud.Allowlist's
// tests say how), so every entry is handed on as the hub reads it.
func TestCloudEgressEntries(t *testing.T) {
	settings := mapSettings{values: map[string]string{
		store.SettingEgressHosts:  `{"hosts":["pypi.org","registry.example.test:8443",".example.test","github.com"]}`,
		store.SettingCloudAddress: "https://hub.example.test/",
	}}
	entries, err := cloudEgressEntries(context.Background(), settings, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api.anthropic.com", ".githubusercontent.com", "pypi.org", "registry.example.test:8443", ".example.test", "hub.example.test"} {
		if !slices.Contains(entries, want) {
			t.Errorf("%s is missing from %v", want, entries)
		}
	}
}

// The loop's token is sent to the cloud address, so one it would cross in
// the clear, or that no cloud session could reach, is refused at use,
// whatever wrote it, and so is every allowlist built from it. Plain HTTP on
// loopback passes only under the tests' opt-in.
func TestCloudAddressFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		stored   string
		loopback bool
		ok       bool
	}{
		{"https://hub.example.test/", false, true},
		{"http://127.0.0.1:9000", false, false},
		{"http://127.0.0.1:9000", true, true},
		{"http://hub.example.test", true, false},
		{"hub.example.test", false, false},
	} {
		settings := mapSettings{values: map[string]string{store.SettingCloudAddress: tc.stored}}
		address, err := cloudAddress(context.Background(), settings, tc.loopback)
		if (err == nil) != tc.ok || (!tc.ok && address != "") {
			t.Errorf("cloudAddress(%q, loopback %v) = %q, %v; want ok %v", tc.stored, tc.loopback, address, err, tc.ok)
		}
		if _, err := cloudEgressEntries(context.Background(), settings, tc.loopback); (err == nil) != tc.ok {
			t.Errorf("cloudEgressEntries with %q, loopback %v: %v, want ok %v", tc.stored, tc.loopback, err, tc.ok)
		}
	}
	if address, err := cloudAddress(context.Background(), mapSettings{}, false); address != "" || err != nil {
		t.Errorf("no address set: %q, %v", address, err)
	}
}
