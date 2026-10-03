package redact

import (
	"context"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

type sourceStore struct {
	store.Store
	loops       []*store.Loop
	connections []*store.Connection
}

func (fake sourceStore) Settings() store.SettingsStore      { return noSettings{} }
func (fake sourceStore) Loops() store.LoopStore             { return listedLoops{loops: fake.loops} }
func (fake sourceStore) LoopSecrets() store.LoopSecretStore { return noLoopSecrets{} }
func (fake sourceStore) Connections() store.ConnectionStore {
	return listedConnections{connections: fake.connections}
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
}

func (listed listedConnections) List(context.Context) ([]*store.Connection, error) {
	return listed.connections, nil
}

type noLoopSecrets struct{ store.LoopSecretStore }

func (noLoopSecrets) List(context.Context, string) ([]*store.LoopSecret, error) { return nil, nil }

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

// Every connection's secret is redacted, attached or not, and a connection
// without one (an mcp-server may have none) adds nothing (ADR-0043).
func TestStoreSourceNamesEveryConnectionSecret(t *testing.T) {
	source := StoreSource{Store: sourceStore{connections: []*store.Connection{
		{Name: "github", Kind: store.ConnectionEnvCredential, Secret: "ghp-synthetic-connection"},
		{Name: "docs", Kind: store.ConnectionMCPServer},
	}}}

	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 || secrets[0] != (Secret{Name: "connection:github", Value: "ghp-synthetic-connection"}) {
		t.Fatalf("secrets = %+v, want only connection:github's", secrets)
	}
}
