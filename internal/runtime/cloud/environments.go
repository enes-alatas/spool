package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/runtime"
)

// DefaultAPIBase is Anthropic's API, where the account's environments are
// listed.
const DefaultAPIBase = "https://api.anthropic.com"

// environmentsEndpoint lists the account's environments. It is the one
// call the hub makes to the API the CLI's own /remote-env reads, which
// Anthropic doesn't document (ADR-0059). The environment pick makes it,
// and so does a start on a login other than the default, which starts
// only in an environment the list shows (ADR-0061, #1078). No start on
// the default login, and no send, depends on it.
const environmentsEndpoint = "/v1/environment_providers"

// Bounds on the list: a page of environments, how many pages are read,
// and the most bytes one page may run to. An account has a handful.
const (
	environmentsPage     = 50
	environmentsPages    = 4
	environmentsMaxBytes = 1 << 20
	environmentsTimeout  = 15 * time.Second
)

// cloudKind is the kind of environment a cloud session runs in on
// Anthropic's machines. The list also carries others, such as a bridge
// to a machine of the operator's, which no cloud loop starts in.
const cloudKind = "anthropic_cloud"

// Environment is one of the account's cloud environments.
type Environment struct {
	ID   string
	Name string
}

// Environments lists the cloud environments of the account of the Claude
// login called login, "" for the default, as the CLI's own /remote-env
// does: by name and ID, in the order the API gives them. It is
// ErrNoAccount with that login not signed in, and an answer it can't read
// is an error naming the endpoint. What the API says is untrusted: the
// hub checks an ID's shape before it keeps one.
func (cloud *Runtime) Environments(ctx context.Context, login string) ([]Environment, error) {
	account, err := cloud.Account(ctx, login)
	if err != nil {
		return nil, err
	}
	var listed []Environment
	afterID := ""
	for range environmentsPages {
		page, err := cloud.environmentsPage(ctx, account, afterID)
		if err != nil {
			return nil, err
		}
		for _, environment := range page.Environments {
			if environment.Kind == cloudKind && environment.State != "archived" {
				listed = append(listed, Environment{ID: environment.ID, Name: environment.Name})
			}
		}
		if !page.HasMore || len(page.Environments) == 0 || page.LastID == "" {
			break
		}
		afterID = page.LastID
	}
	return listed, nil
}

// environmentsAnswer is one page of the list, as much of it as is read.
type environmentsAnswer struct {
	Environments []struct {
		ID    string `json:"environment_id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		State string `json:"state"`
	} `json:"environments"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func (cloud *Runtime) environmentsPage(ctx context.Context, account Account, afterID string) (environmentsAnswer, error) {
	var page environmentsAnswer
	base := cloud.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	query := url.Values{"limit": {fmt.Sprint(environmentsPage)}}
	if afterID != "" {
		query.Set("after_id", afterID)
	}
	ctx, cancel := context.WithTimeout(ctx, environmentsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+environmentsEndpoint+"?"+query.Encode(), nil)
	if err != nil {
		return page, err
	}
	req.Header.Set("Authorization", "Bearer "+account.Token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-organization-uuid", account.OrgUUID)
	resp, err := environmentsClient.Do(req)
	if err != nil {
		return page, fmt.Errorf("listing environments: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, environmentsMaxBytes+1))
	switch {
	case err != nil:
		return page, fmt.Errorf("listing environments: reading the answer: %w", err)
	case len(raw) > environmentsMaxBytes:
		return page, fmt.Errorf("listing environments: the answer ran over %d bytes", environmentsMaxBytes)
	case resp.StatusCode != http.StatusOK:
		return page, fmt.Errorf("listing environments: %s answered %d: %s", environmentsEndpoint, resp.StatusCode, roomtext.Plain(snippet(raw)))
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return page, fmt.Errorf("listing environments: an answer it can't read: %v: %s", err, roomtext.Plain(snippet(raw)))
	}
	return page, nil
}

// environmentsClient follows no redirect: the call carries the account's
// token, and the API has no reason to send it elsewhere.
var environmentsClient = &http.Client{
	Timeout:       environmentsTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// confirmEnvironment checks that a start on a login other than the
// default names an environment of that login's account: a loop on one
// never starts in another account's environment, so a list that can't be
// read holds the start as one that doesn't show the ID does (ADR-0061,
// #1078). The default login's ID was picked from its list or pasted when
// the list failed, and is used as it is.
func (cloud *Runtime) confirmEnvironment(ctx context.Context, source runtime.CloudStart) error {
	if source.Login == "" {
		return nil
	}
	listed, err := cloud.Environments(ctx, source.Login)
	if err != nil {
		return fmt.Errorf("cloud: the environments of Claude login %s can't be listed, so its loop doesn't start until they can: %w", source.Login, err)
	}
	for _, environment := range listed {
		if environment.ID == source.EnvironmentID {
			return nil
		}
	}
	return fmt.Errorf("cloud: %s is not one of the cloud environments of Claude login %s's account", source.EnvironmentID, source.Login)
}
