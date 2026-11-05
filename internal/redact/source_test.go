package redact

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

type sourceStore struct {
	store.Store
	loops       []*store.Loop
	connections []*store.Connection
	retired     []store.RetiredSecret
	settings    map[string]string
	logins      []*store.ClaudeLogin
}

func (fake sourceStore) Settings() store.SettingsStore { return noSettings{values: fake.settings} }
func (fake sourceStore) Loops() store.LoopStore        { return listedLoops{loops: fake.loops} }
func (fake sourceStore) Connections() store.ConnectionStore {
	return listedConnections{connections: fake.connections, retired: fake.retired}
}
func (fake sourceStore) ClaudeLogins() store.ClaudeLoginStore {
	return listedLogins{logins: fake.logins}
}

type listedLogins struct {
	store.ClaudeLoginStore
	logins []*store.ClaudeLogin
}

func (listed listedLogins) List(context.Context) ([]*store.ClaudeLogin, error) {
	return listed.logins, nil
}

type noSettings struct {
	store.SettingsStore
	values map[string]string
}

func (settings noSettings) Get(_ context.Context, key string) (string, error) {
	if value, ok := settings.values[key]; ok {
		return value, nil
	}
	return "", store.ErrNotFound
}

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

// Every Claude login's tokens are redacted by value, the refresh token
// with the access token, and a login signed out holds none (ADR-0060).
func TestStoreSourceNamesEveryClaudeLogin(t *testing.T) {
	source := StoreSource{Store: sourceStore{logins: []*store.ClaudeLogin{
		{Name: "default", Account: store.CloudAccount{Token: "sk-ant-oat01-synthetic-default", RefreshToken: "sk-ant-ort01-synthetic-default"}},
		{Name: "ada", Account: store.CloudAccount{Token: "sk-ant-oat01-synthetic-ada"}},
		{Name: "gone"},
		{Name: "garbled", Unreadable: "sk-ant-oat01-synthetic-garbled"},
	}}}
	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Name: "claude_login", Value: "sk-ant-oat01-synthetic-default"},
		{Name: "claude_login", Value: "sk-ant-ort01-synthetic-default"},
		{Name: "claude_login", Value: "sk-ant-oat01-synthetic-ada"},
		{Name: "claude_login", Value: "sk-ant-oat01-synthetic-garbled"},
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %+v, want %+v", secrets, want)
	}
}

// Either Langfuse key pair reads every trace in its project, so both are
// secrets, and so is each one's secret half alone. The Authorization a
// pair sends, its base64, is masked with it (ADR-0064).
func TestStoreSourceNamesBothLangfuseKeys(t *testing.T) {
	const ingest, read = "pk-lf-synthetic-in:sk-lf-synthetic-in", "pk-lf-synthetic-rd:sk-lf-synthetic-rd"
	source := StoreSource{Store: sourceStore{settings: map[string]string{
		store.SettingLangfuse: `{"relay_upstream":"http://langfuse-web","ingest_key":"` + ingest + `","read_key":"` + read + `"}`,
	}}}
	secrets, err := source.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Secret{
		{Name: store.SettingLangfuse, Value: ingest},
		{Name: store.SettingLangfuse, Value: "sk-lf-synthetic-in"},
		{Name: store.SettingLangfuse, Value: read},
		{Name: store.SettingLangfuse, Value: "sk-lf-synthetic-rd"},
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("secrets = %+v, want %+v", secrets, want)
	}

	redactor := New(source, time.Hour)
	if err := redactor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(ingest))
	if got := redactor.Text(header); strings.Contains(got, base64.StdEncoding.EncodeToString([]byte(ingest))[:24]) {
		t.Errorf("the ingest key's header survives redaction: %q", got)
	}
}

// A key pair the settings let go of still opens its Langfuse project until
// the operator deletes it there, so it stays a secret (ADR-0043).
func TestStoreSourceKeepsRetiredLangfuseKeys(t *testing.T) {
	const current, retired = "pk-lf-synthetic-new:sk-lf-synthetic-new", "pk-lf-synthetic-old:sk-lf-synthetic-old"
	source := StoreSource{Store: sourceStore{settings: map[string]string{
		store.SettingLangfuse: `{"ingest_key":"` + current + `","retired":["` + retired + `"]}`,
	}}}
	redactor := New(source, time.Hour)
	if err := redactor.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := redactor.Text("old pair " + retired); strings.Contains(got, "sk-lf-synthetic-old") {
		t.Errorf("a retired Langfuse key survives redaction: %q", got)
	}
}
