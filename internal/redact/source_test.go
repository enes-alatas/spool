package redact

import (
	"context"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

type sourceStore struct {
	store.Store
	loops []*store.Loop
}

func (s sourceStore) Settings() store.SettingsStore      { return noSettings{} }
func (s sourceStore) Loops() store.LoopStore             { return listedLoops{loops: s.loops} }
func (s sourceStore) LoopSecrets() store.LoopSecretStore { return noLoopSecrets{} }

type noSettings struct{ store.SettingsStore }

func (noSettings) Get(context.Context, string) (string, error) { return "", store.ErrNotFound }

type listedLoops struct {
	store.LoopStore
	loops []*store.Loop
}

func (l listedLoops) List(context.Context) ([]*store.Loop, error) { return l.loops, nil }

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
