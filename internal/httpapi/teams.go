package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/store"
)

// Teams (ADR-0066): named sets of loops on one hub. The operator makes,
// renames and deletes them here; which team a loop is in is a field of the
// loop, set by its create and PATCH. A team's channels and settings are
// later slices of #1289.

const (
	codeTeamNameInvalid = "team_name_invalid"
	codeTeamExists      = "team_exists"
	codeTeamNotFound    = "team_not_found"
	codeTeamHasLoops    = "team_has_loops"
)

// maxTeamDescription bounds a description in characters: one line, as a
// channel's is.
const maxTeamDescription = maxChannelDescription

// teamView is a team as the control room reads it: its loops by name,
// sorted, as the channel view has them.
type teamView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
	Loops       []string `json:"loops"`
}

func (server *Server) teamViews(r *http.Request, teams ...*store.Team) ([]teamView, error) {
	names, err := server.loopNames(r)
	if err != nil {
		return nil, err
	}
	views := make([]teamView, 0, len(teams))
	for _, team := range teams {
		view := teamView{Name: team.Name, Description: team.Description, CreatedAt: team.CreatedAt, Loops: namesOf(names, team.LoopIDs)}
		views = append(views, view)
	}
	return views, nil
}

// loopNames maps every loop's id to its name.
func (server *Server) loopNames(r *http.Request) (map[string]string, error) {
	loops, err := server.Store.Loops().List(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(loops))
	for _, loopRecord := range loops {
		names[loopRecord.ID] = loopRecord.Name
	}
	return names, nil
}

// namesOf is the named loops' names, sorted, leaving out an id no loop has.
func namesOf(names map[string]string, loopIDs []string) []string {
	out := []string{}
	for _, id := range loopIDs {
		if name, ok := names[id]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (server *Server) writeTeam(w http.ResponseWriter, r *http.Request, status int, team *store.Team) {
	views, err := server.teamViews(r, team)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, status, views[0])
}

// teamErr answers a store error about the team named.
func (server *Server) teamErr(w http.ResponseWriter, err error, name string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeTeamNotFound, "team %q not found", name)
	case errors.Is(err, store.ErrDuplicate):
		server.jsonErrCode(w, 409, codeTeamExists, "a team named %q already exists", name)
	default:
		server.jsonErr(w, 500, "%v", err)
	}
}

// teamNameRefusal refuses a name the loop-name rule does not allow
// (ADR-0066).
func teamNameRefusal(name string) *requestError {
	if !nameRe.MatchString(name) {
		return refusalf(400, codeTeamNameInvalid, "a team name must match %s", nameRe.String())
	}
	return nil
}

// teamDescriptionRefusal refuses a description longer or wider than one line.
func teamDescriptionRefusal(description string) *requestError {
	if utf8.RuneCountInString(description) > maxTeamDescription {
		return refusalf(400, "", "a team description is at most %d characters", maxTeamDescription)
	}
	if strings.ContainsAny(description, "\r\n") {
		return refusalf(400, "", "a team description is one line")
	}
	return nil
}

func (server *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := server.Store.Teams().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views, err := server.teamViews(r, teams...)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

func (server *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	team, err := server.Store.Teams().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.teamErr(w, err, r.PathValue("name"))
		return
	}
	server.writeTeam(w, r, 200, team)
}

func (server *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	team := &store.Team{Name: req.Name, Description: strings.TrimSpace(req.Description), CreatedAt: time.Now().UnixMilli()}
	if refusal := teamNameRefusal(team.Name); refusal != nil {
		server.refuse(w, refusal)
		return
	}
	if refusal := teamDescriptionRefusal(team.Description); refusal != nil {
		server.refuse(w, refusal)
		return
	}
	if err := server.Store.Teams().Create(r.Context(), team); err != nil {
		server.teamErr(w, err, team.Name)
		return
	}
	server.writeTeam(w, r, 201, team)
}

// handlePatchTeam renames a team or rewrites its description. Its loops
// stay in it under the new name.
func (server *Server) handlePatchTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Name == nil && req.Description == nil {
		server.jsonErr(w, 400, "nothing to change: a team's name and description are the fields that change")
		return
	}
	edit := store.TeamEdit{Name: req.Name}
	if req.Name != nil {
		if refusal := teamNameRefusal(*req.Name); refusal != nil {
			server.refuse(w, refusal)
			return
		}
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if refusal := teamDescriptionRefusal(description); refusal != nil {
			server.refuse(w, refusal)
			return
		}
		edit.Description = &description
	}
	name := r.PathValue("name")
	team, err := server.Store.Teams().Edit(r.Context(), name, edit)
	if errors.Is(err, store.ErrDuplicate) {
		name = *req.Name
	}
	if err != nil {
		server.teamErr(w, err, name)
		return
	}
	if req.Name != nil {
		server.teamRenamed(r, team)
	}
	server.writeTeam(w, r, 200, team)
}

// handleDeleteTeam deletes a team with no loops in it. One that still has
// loops is refused, naming them: moving each out is the operator choosing
// where it goes (ADR-0066).
func (server *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch err := server.Store.Teams().Delete(r.Context(), name); {
	case errors.Is(err, store.ErrTeamHasLoops):
		server.teamHasLoops(w, r, name)
	case err != nil:
		server.teamErr(w, err, name)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// teamHasLoops refuses deleting a team, naming the loops still in it.
func (server *Server) teamHasLoops(w http.ResponseWriter, r *http.Request, name string) {
	team, err := server.Store.Teams().Get(r.Context(), name)
	if err != nil {
		server.teamErr(w, err, name)
		return
	}
	names, err := server.loopNames(r)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.jsonErrCode(w, 409, codeTeamHasLoops, "team %q still has loops %s; move them out first",
		name, strings.Join(namesOf(names, team.LoopIDs), ", "))
}

// teamRenamed hands each of the team's loops its row as it now stands,
// in it under the new name, as a loop's PATCH does.
func (server *Server) teamRenamed(r *http.Request, team *store.Team) {
	for _, loopID := range team.LoopIDs {
		if updated, err := server.Store.Loops().Get(r.Context(), loopID); err == nil {
			server.Manager.UpdateLoop(updated)
		}
	}
}

// loopTeamRefusal refuses a loop's create or PATCH naming a team the hub
// does not have. "" is no team and always allowed.
func (server *Server) loopTeamRefusal(ctx context.Context, name string) *requestError {
	if name == "" {
		return nil
	}
	if _, err := server.Store.Teams().Get(ctx, name); errors.Is(err, store.ErrNotFound) {
		return refusalf(404, codeTeamNotFound, "team %q not found", name)
	} else if err != nil {
		return refusalf(500, "", "%v", err)
	}
	return nil
}
