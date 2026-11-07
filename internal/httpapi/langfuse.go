package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// codeLangfuseInvalid refuses a Langfuse setting the hub can't act on,
// with the reason as the message for the form to show.
const codeLangfuseInvalid = "langfuse_invalid"

// codeLangfuseFromFile refuses a change to Langfuse settings the hub's
// Langfuse file sets (#1388): the hub reads the file again at its
// next start, which would undo the change.
const codeLangfuseFromFile = "langfuse_from_file"

// langfuseUnsupported is a cloud loop's Langfuse on its view.
const langfuseUnsupported = "unsupported"

// langfuseView is GET /api/settings/langfuse (ADR-0064): the two addresses
// as they are, and each key as whether it is set, never its value.
type langfuseView struct {
	// RelayUpstream is where the trace relay sends a docker loop's traces,
	// as the egress proxy reaches Langfuse; "" is export off.
	RelayUpstream string `json:"relay_upstream"`
	// ReadAddress is where the hub reads figures back, as the hub
	// reaches Langfuse.
	ReadAddress  string `json:"read_address"`
	IngestKeySet bool   `json:"ingest_key_set"`
	ReadKeySet   bool   `json:"read_key_set"`
	// Exporting is whether a docker loop's next session exports its
	// traces: an upstream and an ingest key are both set.
	Exporting bool `json:"exporting"`
	// DefaultRelayUpstream is the upstream the compose profile serves,
	// for the form to offer. It is never applied unasked: an empty
	// upstream is export off.
	DefaultRelayUpstream string `json:"default_relay_upstream"`
	// File is the hub's Langfuse file the settings come from,
	// "" when they were stored here (#1388).
	File string `json:"file"`
}

func newLangfuseView(langfuse store.Langfuse) langfuseView {
	return langfuseView{
		RelayUpstream:        langfuse.RelayUpstream,
		ReadAddress:          langfuse.ReadAddress,
		IngestKeySet:         langfuse.IngestKey != "",
		ReadKeySet:           langfuse.ReadKey != "",
		Exporting:            loop.LangfuseExports(langfuse),
		DefaultRelayUpstream: egress.ComposeLangfuseUpstream,
		File:                 langfuse.File,
	}
}

func (server *Server) handleGetLangfuse(w http.ResponseWriter, r *http.Request) {
	langfuse, err := loop.LoadLangfuse(r.Context(), server.Store.Settings())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, newLangfuseView(langfuse))
}

// putLangfuseReq changes the Langfuse settings: a nil field is left as
// it is, "" clears it, and anything else is validated and stored.
type putLangfuseReq struct {
	RelayUpstream *string `json:"relay_upstream"`
	ReadAddress   *string `json:"read_address"`
	IngestKey     *string `json:"ingest_key"`
	ReadKey       *string `json:"read_key"`
}

// handlePutLangfuse stores the Langfuse settings and hands the relay its
// part. Every field is checked before anything is written, so a refused
// one leaves the rest unapplied too. A failed hand-over still answers with
// what was stored: the relay takes it at the next wake. A session takes
// a change at its next start.
func (server *Server) handlePutLangfuse(w http.ResponseWriter, r *http.Request) {
	var req putLangfuseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	trimmed := func(field *string) *string {
		if field == nil {
			return nil
		}
		value := strings.TrimSpace(*field)
		return &value
	}
	req.RelayUpstream, req.ReadAddress = trimmed(req.RelayUpstream), trimmed(req.ReadAddress)
	req.IngestKey, req.ReadKey = trimmed(req.IngestKey), trimmed(req.ReadKey)
	for _, check := range []struct {
		value *string
		valid func(string) error
	}{
		{req.RelayUpstream, egress.ValidateRelayUpstream},
		{req.ReadAddress, loop.ValidLangfuseReadAddress},
		{req.IngestKey, loop.ValidLangfuseKey},
		{req.ReadKey, loop.ValidLangfuseKey},
	} {
		if check.value == nil || *check.value == "" {
			continue
		}
		if err := check.valid(*check.value); err != nil {
			server.jsonErrCode(w, 400, codeLangfuseInvalid, "%v", err)
			return
		}
	}

	server.settingsMu.Lock()
	defer server.settingsMu.Unlock()
	langfuse, err := loop.LoadLangfuse(r.Context(), server.Store.Settings())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if langfuse.File != "" {
		server.jsonErrCode(w, 409, codeLangfuseFromFile,
			"the Langfuse settings come from %s; edit or remove it and restart the hub", langfuse.File)
		return
	}
	before := langfuse
	changed := false
	for _, field := range []struct {
		value *string
		into  *string
	}{
		{req.RelayUpstream, &langfuse.RelayUpstream},
		{req.ReadAddress, &langfuse.ReadAddress},
		{req.IngestKey, &langfuse.IngestKey},
		{req.ReadKey, &langfuse.ReadKey},
	} {
		if field.value != nil && *field.value != *field.into {
			*field.into, changed = *field.value, true
		}
	}
	if changed {
		// a replaced or cleared key pair stays redacted (ADR-0043)
		langfuse = loop.RetireLangfuseKeys(before, langfuse)
		if err := loop.StoreLangfuse(r.Context(), server.Store.Settings(), langfuse); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		if langfuse.IngestKey != before.IngestKey || langfuse.ReadKey != before.ReadKey {
			server.secretsChanged(r.Context())
		}
		if server.TraceRelay != nil {
			if err := server.TraceRelay(r.Context(), langfuse); err != nil {
				server.Log.Warn("langfuse settings stored but not yet handed to the trace relay; the next wake retries", "err", err)
			}
		}
		// the addresses and whether each key is set, never a key
		server.Log.Info("langfuse settings changed", "relay_upstream", langfuse.RelayUpstream,
			"read_address", langfuse.ReadAddress, "exporting", loop.LangfuseExports(langfuse))
	}
	writeJSON(w, 200, newLangfuseView(langfuse))
}
