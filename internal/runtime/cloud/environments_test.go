package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// The list is read as the CLI's /remote-env reads it, page by page on the
// account's token and organization, and keeps only the environments a
// cloud session starts in.
func TestEnvironmentsListTheAccountsCloudEnvironments(t *testing.T) {
	var asked []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RawQuery)
		if r.URL.Path != environmentsEndpoint || r.Header.Get("Authorization") != "Bearer "+testAccount.Token ||
			r.Header.Get("x-organization-uuid") != testAccount.OrgUUID {
			http.Error(w, "not this", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{"environments":[
				{"kind":"anthropic_cloud","environment_id":"env_01Spool","name":"spool","state":"active"},
				{"kind":"bridge","environment_id":"env_01Laptop","name":"laptop","state":"active"}
			],"has_more":true,"last_id":"env_01Laptop"}`))
			return
		}
		_, _ = w.Write([]byte(`{"environments":[
			{"kind":"anthropic_cloud","environment_id":"env_01Old","name":"old","state":"archived"},
			{"kind":"anthropic_cloud","environment_id":"env_01Other","name":"other","state":"active"}
		],"has_more":false}`))
	}))
	defer api.Close()
	cloud := &Runtime{APIBase: api.URL, Account: func(context.Context, string) (Account, error) { return testAccount, nil }}
	listed, err := cloud.Environments(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Environment{{ID: "env_01Spool", Name: "spool"}, {ID: "env_01Other", Name: "other"}}
	if len(listed) != len(want) || listed[0] != want[0] || listed[1] != want[1] {
		t.Errorf("listed %+v, want %+v", listed, want)
	}
	if len(asked) != 2 || !strings.Contains(asked[1], "after_id=env_01Laptop") {
		t.Errorf("pages asked for: %q, want the second after the first's last id", asked)
	}
}

// A refused list says what the API answered, and no account is
// ErrNoAccount.
func TestEnvironmentsFailLoudly(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	defer api.Close()
	cloud := &Runtime{APIBase: api.URL, Account: func(context.Context, string) (Account, error) { return testAccount, nil }}
	if _, err := cloud.Environments(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "answered 401") ||
		!strings.Contains(err.Error(), "authentication_error") {
		t.Errorf("a refused list: %v, want the status and the answer", err)
	}
	cloud.Account = func(context.Context, string) (Account, error) { return Account{}, ErrNoAccount }
	if _, err := cloud.Environments(context.Background(), ""); !errors.Is(err, ErrNoAccount) {
		t.Errorf("with no account: %v, want ErrNoAccount", err)
	}
}

// A start lists the account's environments of its loop's login, on that
// login's own token, and goes ahead only in one the list shows; a list
// that can't be read holds it too. The default's start lists as any
// other's (#1078, #1232).
func TestAStartNeedsAnEnvironmentOfItsLoginsAccount(t *testing.T) {
	ada := Account{Token: "sk-ant-oat01-fixture-ada", OrgUUID: "00000000-0000-4000-8000-00000000ada0"}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") == "Bearer "+ada.Token && r.Header.Get("x-organization-uuid") == ada.OrgUUID:
			_, _ = w.Write([]byte(`{"environments":[{"kind":"anthropic_cloud","environment_id":"env_01Ada","name":"ada","state":"active"}]}`))
		case r.Header.Get("Authorization") == "Bearer "+testAccount.Token:
			_, _ = w.Write([]byte(`{"environments":[{"kind":"anthropic_cloud","environment_id":"env_01Spool","name":"spool","state":"active"}]}`))
		default:
			http.Error(w, "no such account", http.StatusUnauthorized)
		}
	}))
	defer api.Close()
	var asked []string
	cloud := &Runtime{APIBase: api.URL, Account: func(_ context.Context, login string) (Account, error) {
		asked = append(asked, login)
		if login == "ada" {
			return ada, nil
		}
		return testAccount, nil
	}}
	ctx := context.Background()

	if err := cloud.confirmEnvironment(ctx, runtime.CloudStart{EnvironmentID: "env_01Spool"}); err != nil {
		t.Errorf("the default's start in its environment: %v", err)
	}
	if err := cloud.confirmEnvironment(ctx, runtime.CloudStart{EnvironmentID: "env_01Anything"}); err == nil ||
		!strings.Contains(err.Error(), "env_01Anything is not one of the cloud environments of Claude login default's account") {
		t.Errorf("the default's start in an environment its account doesn't hold: %v, want it held", err)
	}
	if err := cloud.confirmEnvironment(ctx, runtime.CloudStart{EnvironmentID: "env_01Ada", Login: "ada"}); err != nil {
		t.Errorf("ada's start in ada's environment: %v", err)
	}
	if err := cloud.confirmEnvironment(ctx, runtime.CloudStart{EnvironmentID: "env_01Spool", Login: "ada"}); err == nil ||
		!strings.Contains(err.Error(), "env_01Spool is not one of the cloud environments of Claude login ada's account") {
		t.Errorf("ada's start in another account's environment: %v, want it held, naming both", err)
	}
	if want := []string{"", "", "ada", "ada"}; !slices.Equal(asked, want) {
		t.Errorf("accounts read: %q, want %q, each start's own login's", asked, want)
	}

	cloud.Account = func(context.Context, string) (Account, error) { return Account{}, ErrNoAccount }
	if err := cloud.confirmEnvironment(ctx, runtime.CloudStart{EnvironmentID: "env_01Ada", Login: "ada"}); !errors.Is(err, ErrNoAccount) ||
		!strings.Contains(err.Error(), "can't be listed") {
		t.Errorf("ada signed out: %v, want the start held for a list that can't be read", err)
	}
}
