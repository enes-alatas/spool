package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// The model list (#332): the family aliases and the operator's custom
// entries, each with what it runs as on this hub (ADR-0033). The list only
// feeds the dropdowns; a loop's model is whatever it was saved with.

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	view, err := s.Models.View(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

type customModelReq struct {
	Model string  `json:"model"`
	Label *string `json:"label"`
}

func (s *Server) handleAddCustomModel(w http.ResponseWriter, r *http.Request) {
	var req customModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	label := ""
	if req.Label != nil {
		label = *req.Label
	}
	entry, err := s.Models.Add(r.Context(), req.Model, label)
	switch {
	case errors.Is(err, loop.ErrInvalidModel):
		s.jsonErr(w, 400, "%v", err)
	case errors.Is(err, store.ErrDuplicate):
		s.jsonErr(w, 409, "%s is already on the model list", req.Model)
	case err != nil:
		s.jsonErr(w, 500, "%v", err)
	default:
		writeJSON(w, 201, entry)
	}
}

func (s *Server) handlePatchCustomModel(w http.ResponseWriter, r *http.Request) {
	var req customModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Model != "" {
		s.jsonErr(w, 400, "an entry's model cannot be edited: delete it and add another")
		return
	}
	if req.Label == nil {
		s.jsonErr(w, 400, "nothing to change: label is the one editable field")
		return
	}
	entry, err := s.Models.Relabel(r.Context(), r.PathValue("id"), *req.Label)
	switch {
	case errors.Is(err, loop.ErrInvalidModel):
		s.jsonErr(w, 400, "%v", err)
	case err != nil:
		s.storeErr(w, err, "custom model")
	default:
		writeJSON(w, 200, entry)
	}
}

func (s *Server) handleDeleteCustomModel(w http.ResponseWriter, r *http.Request) {
	err := s.Models.Delete(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err, "custom model")
		return
	}
	w.WriteHeader(204)
}
