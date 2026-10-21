package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/store"
)

// Settings → Cloud's environment step (ADR-0059): every cloud loop starts
// in one environment the operator sets up on claude.ai, picked here by
// name from the account's list, or pasted as an ID when the list fails.
// The hub can't read the environment's allowed hosts back, so it serves
// the text to paste there and keeps whether the operator confirmed
// pasting the current one.

// CloudEnvironments lists the account's cloud environments, through the
// one read-only call ADR-0059 keeps on the API the CLI's /remote-env
// reads. Nil on a hub that wires none: the page falls back to the paste.
type CloudEnvironments interface {
	Environments(ctx context.Context) ([]CloudEnvironment, error)
}

// CloudEnvironment is one of the account's cloud environments.
type CloudEnvironment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// The reasons the environment step refuses.
const (
	// codeCloudEnvironmentsUnlisted: the account's list couldn't be read,
	// so the page offers the paste instead.
	codeCloudEnvironmentsUnlisted = "cloud_environments_unlisted"
	// codeCloudEnvironmentInvalid: a chosen ID without an environment ID's
	// shape.
	codeCloudEnvironmentInvalid = "cloud_environment_invalid"
	// codeCloudEnvironmentUnlisted: a chosen ID the account's list was
	// read and doesn't show: a bridge to one of the operator's machines,
	// an archived environment, or another account's.
	codeCloudEnvironmentUnlisted = "cloud_environment_unlisted"
	// codeCloudAllowlistStale: a confirm of text that is no longer the
	// list to paste, or with no environment chosen.
	codeCloudAllowlistStale = "cloud_allowlist_stale"
)

// maxCloudEnvironmentName caps a listed name, in runes.
const maxCloudEnvironmentName = 200

// cloudEnvironmentView is the environment step as Settings → Cloud shows
// it. Allowlist is built from the current allowlist on every request and
// never cached, so the page and the hub can't disagree about it.
type cloudEnvironmentView struct {
	// ID is the chosen environment, "" while none is.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Allowlist is the text to paste into the environment's allowed
	// hosts, one host per line: the public address's host, then the hub's
	// egress allowlist in an environment's spelling.
	Allowlist string `json:"allowlist"`
	// NotApplied are the allowlist's entries an environment can't carry.
	NotApplied []string `json:"not_applied"`
	// Digest is Allowlist's SHA-256 in hex, which a confirm names.
	Digest string `json:"digest"`
	// Confirmed is whether the operator confirmed pasting this Allowlist
	// into this environment. Until they do, cloud loops carry a warning.
	Confirmed   bool  `json:"confirmed"`
	ConfirmedAt int64 `json:"confirmed_at,omitempty"`
}

type cloudEnvironmentReq struct {
	ID string `json:"id"`
}

type cloudAllowlistConfirmReq struct {
	Digest string `json:"digest"`
}

func (server *Server) handleListCloudEnvironments(w http.ResponseWriter, r *http.Request) {
	listed, err := server.listCloudEnvironments(r.Context())
	if err != nil {
		server.jsonErrCode(w, http.StatusBadGateway, codeCloudEnvironmentsUnlisted, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"environments": listed})
}

// listCloudEnvironments is the account's list, without an ID of the
// wrong shape: what the API says is untrusted, and the page must never
// offer an ID the hub would refuse. A name is the API's free text too,
// so it is capped and loses its backticks.
func (server *Server) listCloudEnvironments(ctx context.Context) ([]CloudEnvironment, error) {
	if server.CloudEnvironments == nil {
		return nil, errors.New("this hub can't list environments; paste the ID")
	}
	listed, err := server.CloudEnvironments.Environments(ctx)
	if err != nil {
		return nil, err
	}
	kept := []CloudEnvironment{}
	for _, environment := range listed {
		if store.ValidCloudEnvironmentID(environment.ID) {
			if name := []rune(environment.Name); len(name) > maxCloudEnvironmentName {
				environment.Name = string(name[:maxCloudEnvironmentName])
			}
			environment.Name = roomtext.Plain(environment.Name)
			kept = append(kept, environment)
		}
	}
	return kept, nil
}

// handleChooseCloudEnvironment stores the environment every cloud loop
// starts in. When the account's list is read, the ID must be on it, and
// its name is the one the list gives, never the page's word for it. A
// bare paste is taken only when the list fails, and stored with no name;
// the ID already stored keeps its name then.
// Another ID than the one stored drops the confirm.
func (server *Server) handleChooseCloudEnvironment(w http.ResponseWriter, r *http.Request) {
	var req cloudEnvironmentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	id := strings.TrimSpace(req.ID)
	if !store.ValidCloudEnvironmentID(id) {
		server.jsonErrCode(w, 400, codeCloudEnvironmentInvalid, "%q is not an environment ID: env_ and letters and digits", req.ID)
		return
	}
	name := ""
	listed, listErr := server.listCloudEnvironments(r.Context())
	if listErr == nil {
		found := false
		for _, environment := range listed {
			if environment.ID == id {
				name, found = environment.Name, true
			}
		}
		if !found {
			server.jsonErrCode(w, 400, codeCloudEnvironmentUnlisted, "%s is not one of this account's cloud environments", id)
			return
		}
	}
	server.changeCloudEnvironment(w, r, func(chosen *store.CloudEnvironment) error {
		if chosen.ID != id {
			*chosen = store.CloudEnvironment{ID: id}
		}
		// a list that failed leaves the same ID its stored name
		if listErr == nil {
			chosen.Name = name
		}
		return nil
	})
}

func (server *Server) handleClearCloudEnvironment(w http.ResponseWriter, r *http.Request) {
	server.changeCloudEnvironment(w, r, func(chosen *store.CloudEnvironment) error {
		*chosen = store.CloudEnvironment{}
		return nil
	})
}

// handleConfirmCloudAllowlist records that the operator pasted the list
// whose digest the request names. A digest that isn't the current list's
// is refused: the list moved since the page showed it (ADR-0059).
func (server *Server) handleConfirmCloudAllowlist(w http.ResponseWriter, r *http.Request) {
	var req cloudAllowlistConfirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	server.changeCloudEnvironment(w, r, func(chosen *store.CloudEnvironment) error {
		if chosen.ID == "" {
			return staleConfirmError("choose the environment first")
		}
		allowlist, err := server.cloudAllowlist(r.Context())
		if err != nil {
			return err
		}
		if req.Digest != allowlist.digest() {
			return staleConfirmError("the list to paste has changed since the page showed it; paste the list shown now and confirm again")
		}
		chosen.ConfirmedDigest, chosen.ConfirmedAt = req.Digest, time.Now().UnixMilli()
		return nil
	})
}

// changeCloudEnvironment applies change to the stored environment and
// answers with Settings → Cloud. A staleConfirmError change returns is the
// confirm's refusal, any other error a failure; either way nothing is
// stored.
func (server *Server) changeCloudEnvironment(w http.ResponseWriter, r *http.Request, change func(*store.CloudEnvironment) error) {
	server.cloudEnvironmentMu.Lock()
	defer server.cloudEnvironmentMu.Unlock()
	chosen, err := server.storedCloudEnvironment(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if err := change(&chosen); err != nil {
		var stale staleConfirmError
		if errors.As(err, &stale) {
			server.jsonErrCode(w, 409, codeCloudAllowlistStale, "%v", err)
		} else {
			server.jsonErr(w, 500, "%v", err)
		}
		return
	}
	raw := ""
	if chosen.ID != "" {
		encoded, err := json.Marshal(chosen)
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		raw = string(encoded)
	}
	if err := server.Store.Settings().Set(r.Context(), store.SettingCloudEnvironment, raw); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if server.CloudGate != nil {
		server.CloudGate.SetupChanged()
	}
	server.writeCloudSettings(w, r)
}

// staleConfirmError is a confirm the hub refuses: none chosen, or a list
// that moved since the page showed it.
type staleConfirmError string

func (reason staleConfirmError) Error() string { return string(reason) }

// storedCloudEnvironment is the environment as stored, the zero one for
// none.
func (server *Server) storedCloudEnvironment(ctx context.Context) (store.CloudEnvironment, error) {
	var chosen store.CloudEnvironment
	raw, err := server.Store.Settings().Get(ctx, store.SettingCloudEnvironment)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return chosen, nil
	}
	if err != nil {
		return chosen, err
	}
	if err := json.Unmarshal([]byte(raw), &chosen); err != nil {
		return chosen, err
	}
	return chosen, nil
}

// cloudAllowlistText is the list to paste and the entries left off it.
type cloudAllowlistText struct {
	hosts, notApplied []string
}

func (allowlist cloudAllowlistText) text() string { return strings.Join(allowlist.hosts, "\n") }

func (allowlist cloudAllowlistText) digest() string {
	sum := sha256.Sum256([]byte(allowlist.text()))
	return hex.EncodeToString(sum[:])
}

// cloudAllowlist builds the list to paste from the allowlist as it is now:
// the public address's host, which every session's hooks post to, then
// ADR-0028's built-in hosts and the stored ones, as an environment spells
// them (egress.EnvironmentHosts).
func (server *Server) cloudAllowlist(ctx context.Context) (cloudAllowlistText, error) {
	var entries []string
	address, err := server.storedCloudAddress(ctx)
	if err != nil {
		return cloudAllowlistText{}, err
	}
	if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" {
		entries = append(entries, parsed.Hostname())
	}
	extra, err := server.egressHosts(ctx)
	if err != nil {
		return cloudAllowlistText{}, err
	}
	entries = append(append(entries, egress.DefaultHosts()...), extra.Hosts...)
	hosts, notApplied := egress.EnvironmentHosts(entries)
	return cloudAllowlistText{hosts: hosts, notApplied: notApplied}, nil
}

// cloudEnvironment is the environment step as Settings → Cloud shows it.
func (server *Server) cloudEnvironment(ctx context.Context) (cloudEnvironmentView, error) {
	chosen, err := server.storedCloudEnvironment(ctx)
	if err != nil {
		return cloudEnvironmentView{}, err
	}
	allowlist, err := server.cloudAllowlist(ctx)
	if err != nil {
		return cloudEnvironmentView{}, err
	}
	view := cloudEnvironmentView{
		ID: chosen.ID, Name: chosen.Name,
		Allowlist: allowlist.text(), NotApplied: allowlist.notApplied, Digest: allowlist.digest(),
	}
	if view.NotApplied == nil {
		view.NotApplied = []string{}
	}
	if chosen.ID != "" && chosen.ConfirmedDigest == view.Digest {
		view.Confirmed, view.ConfirmedAt = true, chosen.ConfirmedAt
	}
	return view, nil
}

// cloudAllowlistUnconfirmed reports whether cloud loops carry the warning
// that the hub's egress allowlist doesn't bind them: no environment is
// chosen, or the list to paste isn't the one confirmed (ADR-0059).
func (server *Server) cloudAllowlistUnconfirmed(ctx context.Context) bool {
	view, err := server.cloudEnvironment(ctx)
	return err != nil || !view.Confirmed
}
