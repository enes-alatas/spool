package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	cloud := &Runtime{APIBase: api.URL, Account: func(context.Context) (Account, error) { return testAccount, nil }}
	listed, err := cloud.Environments(context.Background())
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
	cloud := &Runtime{APIBase: api.URL, Account: func(context.Context) (Account, error) { return testAccount, nil }}
	if _, err := cloud.Environments(context.Background()); err == nil || !strings.Contains(err.Error(), "answered 401") ||
		!strings.Contains(err.Error(), "authentication_error") {
		t.Errorf("a refused list: %v, want the status and the answer", err)
	}
	cloud.Account = func(context.Context) (Account, error) { return Account{}, ErrNoAccount }
	if _, err := cloud.Environments(context.Background()); !errors.Is(err, ErrNoAccount) {
		t.Errorf("with no account: %v, want ErrNoAccount", err)
	}
}
