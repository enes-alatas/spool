package redact

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

type sourceStore struct {
	store.Store
	loops       []*store.Loop
	connections []*store.Connection
	retired     []store.RetiredSecret
}

func (fake sourceStore) Settings() store.SettingsStore { return noSettings{} }
func (fake sourceStore) Loops() store.LoopStore        { return listedLoops{loops: fake.loops} }
func (fake sourceStore) Connections() store.ConnectionStore {
	return listedConnections{connections: fake.connections, retired: fake.retired}
}

type noSettings struct{ store.SettingsStore }

func (noSettings) Get(context.Context, string) (string, error) { return "", store.ErrNotFound }

type listedLoops struct {
	store.LoopStore
	loops []*store.Loop
}

func (listed listedLoops) List(context.Context) ([]*store.Loop, error) { return listed.loops, nil }

type listedConnections struct {
	store.ConnectionStore
	connections []*store.Connection
	retired     []store.RetiredSecret
}

func (listed listedConnections) List(context.Context) ([]*store.Connection, error) {
	return listed.connections, nil
}

func (listed listedConnections) Retired(context.Context) ([]store.RetiredSecret, error) {
	return listed.retired, nil
}

// Every surface credential a loop holds is a secret the redactor knows by
// value: a Telegram bot token, and both of a Slack app's tokens, since
// either one opens the app (#230).
func TestStoreSourceNamesEverySurfaceCredential(t *testing.T) {
	source := StoreSource{Store: sourceStore{loops: []*store.Loop{
		{ID: "tg", TGBotToken: "tg-synthetic-token"},
		{ID: "slack", SlackAppToken: "xapp-synthetic-token", SlackBotToken: "xoxb-synthetic-token"},
	}}}

	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, secret := range secrets {
		got[secret.Name] = secret.Value
	}
	want := map[string]string{
		"tg_bot_token":    "tg-synthetic-token",
		"slack_app_token": "xapp-synthetic-token",
		"slack_bot_token": "xoxb-synthetic-token",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("secret %s = %q, want %q", name, got[name], value)
		}
	}
}

// Every connection's secret is redacted, attached or not: an env-var's
// under its variable's name, another kind's under the connection's. A
// connection without one (an mcp-server may have none) adds nothing
// (ADR-0043).
func TestStoreSourceNamesEveryConnectionSecret(t *testing.T) {
	source := StoreSource{Store: sourceStore{connections: []*store.Connection{
		{Name: "github", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "ghp-synthetic-connection"},
		{Name: "search", Kind: store.ConnectionMCPServer, Secret: "mcp-synthetic-connection"},
		{Name: "docs", Kind: store.ConnectionMCPServer},
	}}}

	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Name: "GH_TOKEN", Value: "ghp-synthetic-connection"},
		{Name: "connection:search", Value: "mcp-synthetic-connection"},
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %+v, want %+v", secrets, want)
	}
}

// A value a connection let go of, replaced or deleted, stays redacted under
// the name it had while live: a credential the hub no longer holds may
// still open something upstream (#609).
func TestStoreSourceKeepsRetiredValues(t *testing.T) {
	source := StoreSource{Store: sourceStore{
		connections: []*store.Connection{
			{Name: "github", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "ghp-synthetic-new"},
		},
		retired: []store.RetiredSecret{
			{Connection: "github", RedactName: "GH_TOKEN", Value: "ghp-synthetic-old"},
			{Connection: "search", RedactName: "connection:search", Value: "mcp-synthetic-deleted"},
		},
	}}

	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Name: "GH_TOKEN", Value: "ghp-synthetic-new"},
		{Name: "GH_TOKEN", Value: "ghp-synthetic-old"},
		{Name: "connection:search", Value: "mcp-synthetic-deleted"},
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %+v, want %+v", secrets, want)
	}
}

type failingSource struct{}

func (failingSource) Secrets(context.Context) ([]Secret, error) {
	return nil, errors.New("store unavailable")
}

// Sources is each source's secrets together, and one failing source fails
// the load, so the redactor keeps its last snapshot rather than dropping the
// secrets that source held.
func TestSourcesJoinsEachSource(t *testing.T) {
	sources := Sources{
		Fixed{{Name: "operator_token", Value: "op-synthetic-token"}},
		Fixed{{Name: "hub_key", Value: "hub-synthetic-key"}},
	}
	secrets, err := sources.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Name: "operator_token", Value: "op-synthetic-token"},
		{Name: "hub_key", Value: "hub-synthetic-key"},
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %+v, want %+v", secrets, want)
	}

	if _, err := append(sources, failingSource{}).Secrets(context.Background()); err == nil {
		t.Fatal("a failing source did not fail the load")
	}
}
