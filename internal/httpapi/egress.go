package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/store"
)

// --- egress settings ---
//
// The operator's extra egress hosts (#542): on top of the built-in
// allowlist, reachable by every docker loop, edited through these routes and
// applied without a hub restart. The hub stores the list and hands it to the
// egress wall, which copies it into the proxy (ADR-0028).

// EgressWall is the part of the docker runtime these routes drive.
type EgressWall interface {
	// EgressEnabled reports whether workstation egress is filtered at all.
	EgressEnabled() bool
	// EgressGateway is the entry the hub is reached by, "" when unfiltered.
	EgressGateway() string
	// SetFleetEgress applies the extra hosts to the proxy, or holds them
	// until there is one.
	SetFleetEgress(ctx context.Context, entries []string) error
	// FleetEgressApplied is when the proxy took the current list, zero when
	// it hasn't.
	FleetEgressApplied() time.Time
}

// codeEgressHostInvalid refuses an entry the allowlist can't act on, with
// the reason as the message for the form to show.
const codeEgressHostInvalid = "egress_host_invalid"

// gatewayReason is the built-in group the hub's own entry is shown in.
const gatewayReason = "The hub: the loop's MCP tools and hooks reach Spool on this one port of your machine, and on no other."

// egressView is GET /api/settings/egress, and the answer to every change.
type egressView struct {
	Enforced  bool               `json:"enforced"`
	BuiltIn   []egress.HostGroup `json:"built_in"`
	Extra     []string           `json:"extra"`
	Flag      []string           `json:"flag,omitzero"` // [] for a hub started with an empty flag
	ChangedAt int64              `json:"changed_at,omitempty"`
	AppliedAt int64              `json:"applied_at,omitempty"`
}

type addEgressHostReq struct {
	Host string `json:"host"`
}

func (server *Server) handleGetEgress(w http.ResponseWriter, r *http.Request) {
	hosts, err := server.egressHosts(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, server.egressView(hosts))
}

// handleAddEgressHost adds one host. A port is refused: these routes name
// hosts, and a port beyond 80 and 443 stays a decision taken at the
// terminal with --egress-allow. A host already listed changes nothing.
func (server *Server) handleAddEgressHost(w http.ResponseWriter, r *http.Request) {
	var req addEgressHostReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	entry, err := egress.Canonical(req.Host)
	if err != nil {
		server.jsonErrCode(w, 400, codeEgressHostInvalid, "%v", err)
		return
	}
	if egress.HasPort(entry) {
		server.jsonErrCode(w, 400, codeEgressHostInvalid,
			"%q: a host without a port; ports are set with --egress-allow when the hub starts", req.Host)
		return
	}
	server.changeEgressHosts(w, r, func(hosts []string) []string {
		if slices.Contains(hosts, entry) {
			return hosts
		}
		return append(hosts, entry)
	})
}

// handleRemoveEgressHost removes one entry, as the view lists it. One not
// on the list changes nothing.
func (server *Server) handleRemoveEgressHost(w http.ResponseWriter, r *http.Request) {
	entry := r.PathValue("entry")
	if canonical, err := egress.Canonical(entry); err == nil {
		entry = canonical
	}
	server.changeEgressHosts(w, r, func(hosts []string) []string {
		return slices.DeleteFunc(hosts, func(host string) bool { return host == entry })
	})
}

// changeEgressHosts stores the list change makes of the current one and
// applies it. A list that didn't change is neither stored nor applied, so
// its changed_at stays the moment it last did. A failed apply still answers
// with the stored list: the proxy takes it at the next wake, and until then
// the view's applied_at says it hasn't.
func (server *Server) changeEgressHosts(w http.ResponseWriter, r *http.Request, change func([]string) []string) {
	server.egressMu.Lock()
	defer server.egressMu.Unlock()
	hosts, err := server.egressHosts(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	next := change(slices.Clone(hosts.Hosts))
	if !slices.Equal(next, hosts.Hosts) {
		hosts = store.EgressHosts{Hosts: next, ChangedAt: time.Now().UnixMilli()}
		if err := storeEgressHosts(r.Context(), server.Store.Settings(), hosts); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		if server.Egress != nil {
			if err := server.Egress.SetFleetEgress(r.Context(), hosts.Hosts); err != nil {
				server.Log.Warn("egress hosts stored but not yet applied; the next wake retries", "err", err)
			}
		}
	}
	writeJSON(w, 200, server.egressView(hosts))
}

func (server *Server) egressHosts(ctx context.Context) (store.EgressHosts, error) {
	hosts, _, err := loadEgressHosts(ctx, server.Store.Settings())
	return hosts, err
}

func (server *Server) egressView(hosts store.EgressHosts) egressView {
	view := egressView{
		BuiltIn:   egress.BuiltIn,
		Extra:     hosts.Hosts,
		ChangedAt: hosts.ChangedAt,
	}
	if view.Extra == nil {
		view.Extra = []string{}
	}
	if server.EgressFlag != nil && !slices.Equal(server.EgressFlag, hosts.Hosts) {
		view.Flag = server.EgressFlag
	}
	if server.Egress == nil || !server.Egress.EgressEnabled() {
		return view
	}
	view.Enforced = true
	if gateway := server.Egress.EgressGateway(); gateway != "" {
		view.BuiltIn = append(slices.Clone(egress.BuiltIn), egress.HostGroup{Reason: gatewayReason, Hosts: []string{gateway}})
	}
	if applied := server.Egress.FleetEgressApplied(); !applied.IsZero() {
		view.AppliedAt = applied.UnixMilli()
	}
	return view
}

// SeedEgressHosts is the extra hosts a starting hub applies: the stored
// list, or on the first start, flag's, which it stores and reports as
// seeded. From then on the stored list wins, and the view shows flag
// beside it when they differ. flag's entries are canonical and distinct,
// as the stored ones are.
func SeedEgressHosts(ctx context.Context, settings store.SettingsStore, flag []string) (hosts []string, seeded bool, err error) {
	stored, found, err := loadEgressHosts(ctx, settings)
	if err != nil || found {
		return stored.Hosts, false, err
	}
	stored = store.EgressHosts{Hosts: slices.Clone(flag), ChangedAt: time.Now().UnixMilli()}
	return stored.Hosts, true, storeEgressHosts(ctx, settings, stored)
}

// loadEgressHosts reads the stored extra hosts; found is false when the
// hub has never stored any.
func loadEgressHosts(ctx context.Context, settings store.SettingsStore) (hosts store.EgressHosts, found bool, err error) {
	raw, err := settings.Get(ctx, store.SettingEgressHosts)
	if errors.Is(err, store.ErrNotFound) {
		return store.EgressHosts{}, false, nil
	}
	if err != nil {
		return store.EgressHosts{}, false, err
	}
	if err := json.Unmarshal([]byte(raw), &hosts); err != nil {
		return store.EgressHosts{}, false, err
	}
	return hosts, true, nil
}

// storeEgressHosts writes the extra hosts.
func storeEgressHosts(ctx context.Context, settings store.SettingsStore, hosts store.EgressHosts) error {
	raw, err := json.Marshal(hosts)
	if err != nil {
		return err
	}
	return settings.Set(ctx, store.SettingEgressHosts, string(raw))
}
