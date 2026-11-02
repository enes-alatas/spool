package httpapi

import (
	"errors"
	"net/http"

	"github.com/enes-alatas/spool/internal/promptschat"
)

// Reasons an import answers with, sent as "code" (ADR-0063).
const (
	// The URL or the page it names is no bot on prompts.chat.
	codeImportRefused = "import_refused"
	// prompts.chat didn't answer the fetch.
	codeImportFailed = "import_failed"
	// The hub wires no fetcher.
	codeImportOff = "import_off"
)

// handleImportPromptsChat answers the loop draft of the prompts.chat bot
// at ?url=, for the operator to edit before creating the loop. It fetches
// that bot's .json from prompts.chat and nowhere else, and creates
// nothing (ADR-0063).
func (server *Server) handleImportPromptsChat(w http.ResponseWriter, r *http.Request) {
	if server.PromptsChat == nil {
		server.jsonErrCode(w, http.StatusServiceUnavailable, codeImportOff, "this hub doesn't import from %s", promptschat.Host)
		return
	}
	draft, err := promptschat.Import(r.Context(), server.PromptsChat, r.URL.Query().Get("url"))
	var refusal *promptschat.Refusal
	switch {
	case errors.As(err, &refusal):
		server.jsonErrCode(w, http.StatusBadRequest, codeImportRefused, "%s", refusal.Reason)
		return
	case err != nil:
		server.Log.Warn("prompts.chat import", "err", err)
		server.jsonErrCode(w, http.StatusBadGateway, codeImportFailed, "%s didn't answer; try again in a minute", promptschat.Host)
		return
	}
	if draft.Runtime == "" {
		draft.Runtime = server.defaultRuntime()
	}
	writeJSON(w, http.StatusOK, draft)
}
