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
// loop, set by its create and PATCH. A team's channels are channels with
// a team (channels.go), its home channel among them; its settings are
// later slices of #1289.

const (
	codeTeamNameInvalid = "team_name_invalid"
	codeTeamExists      = "team_exists"
	codeTeamNotFound    = "team_not_found"
	codeTeamHasLoops    = "team_has_loops"
	// A home channel must be one of the team's own.
	codeChannelNotInTeam = "channel_not_in_team"
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
	// HomeChannel is the channel every loop put in the team joins, ""
	// for none; Channels are all of the team's, by name.
	HomeChannel string   `json:"home_channel"`
	Channels    []string `json:"channels"`
	// ClaudeLogin is the Claude login the team's contained loops run on
	// when they name none, "" for the hub's default (ADR-0066).
	ClaudeLogin string `json:"claude_login"`
}

func (server *Server) teamViews(r *http.Request, teams ...*store.Team) ([]teamView, error) {
	names, err := server.loopNames(r)
	if err != nil {
		return nil, err
	}
	views := make([]teamView, 0, len(teams))
	for _, team := range teams {
		view := teamView{Name: team.Name, Description: team.Description, CreatedAt: team.CreatedAt, Loops: namesOf(names, team.LoopIDs),
			HomeChannel: team.HomeChannel, Channels: team.Channels, ClaudeLogin: team.ClaudeLogin}
		if view.Channels == nil {
			view.Channels = []string{}
		}
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
		// HomeChannel names the channel made with the team, the team's own
		// name when absent.
		HomeChannel *string `json:"home_channel"`
		// ClaudeLogin is the team's default Claude login, "" or "default"
		// for the hub's.
		ClaudeLogin string `json:"claude_login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	team := &store.Team{Name: req.Name, Description: strings.TrimSpace(req.Description), CreatedAt: time.Now().UnixMilli(),
		ClaudeLogin: loopLoginName(req.ClaudeLogin)}
	if refusal := teamNameRefusal(team.Name); refusal != nil {
		server.refuse(w, refusal)
		return
	}
	if refusal := teamDescriptionRefusal(team.Description); refusal != nil {
		server.refuse(w, refusal)
		return
	}
	team.HomeChannel = team.Name
	if req.HomeChannel != nil {
		team.HomeChannel = *req.HomeChannel
	}
	switch {
	case team.HomeChannel == store.RetiredChannel:
		server.jsonErrCode(w, 400, codeChannelReserved, "%q was the fleet channel's name, and no channel takes it again; name the team's home channel with home_channel", store.RetiredChannel)
		return
	case !store.ValidChannelName(team.HomeChannel):
		server.jsonErrCode(w, 400, codeChannelNameInvalid,
			"the home channel %q is not a channel name, which is 1 to 32 of a-z, 0-9 and '-'; name it with home_channel", team.HomeChannel)
		return
	}
	refusal, err := server.putOnLogin(r.Context(), team.ClaudeLogin, func() error {
		return server.Store.Teams().Create(r.Context(), team)
	})
	if refusal != nil {
		server.refuse(w, refusal)
		return
	}
	if err != nil {
		if errors.Is(err, store.ErrChannelTaken) {
			server.jsonErrCode(w, 409, codeChannelExists, "a channel named %q already exists; name the team's home channel with home_channel", team.HomeChannel)
			return
		}
		server.teamErr(w, err, team.Name)
		return
	}
	server.writeTeam(w, r, 201, team)
}

// handlePatchTeam renames a team, rewrites its description, or names its
// home channel. Its loops and channels stay in it under a new name.
func (server *Server) handlePatchTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		HomeChannel *string `json:"home_channel"`
		ClaudeLogin *string `json:"claude_login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Name == nil && req.Description == nil && req.HomeChannel == nil && req.ClaudeLogin == nil {
		server.jsonErr(w, 400, "nothing to change: a team's name, description, home channel and Claude login are the fields that change")
		return
	}
	edit := store.TeamEdit{Name: req.Name, HomeChannel: req.HomeChannel, At: time.Now().UnixMilli()}
	putOn := ""
	if req.ClaudeLogin != nil {
		putOn = loopLoginName(*req.ClaudeLogin)
		edit.ClaudeLogin = &putOn
	}
	if req.HomeChannel != nil && *req.HomeChannel != "" {
		if _, err := server.Store.Channels().Get(r.Context(), *req.HomeChannel); errors.Is(err, store.ErrNotFound) {
			server.jsonErrCode(w, 404, codeChannelNotFound, "channel %q not found", *req.HomeChannel)
			return
		} else if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
	}
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
	wasLogin, team, refusal, err := server.editTeam(r.Context(), name, putOn, edit)
	if refusal != nil {
		server.refuse(w, refusal)
		return
	}
	switch {
	case errors.Is(err, store.ErrChannelHasOutsiders):
		server.channelHasOutsiders(w, r, *req.HomeChannel, name)
		return
	case errors.Is(err, store.ErrChannelInOtherTeam):
		server.jsonErrCode(w, 409, codeChannelInOtherTeam, "channel %q is another team's, so it can't be team %q's home", *req.HomeChannel, name)
		return
	case errors.Is(err, store.ErrChannelNotInTeam):
		server.jsonErrCode(w, 409, codeChannelNotInTeam, "channel %q is not team %q's; give it to the team first", *req.HomeChannel, name)
		return
	}
	if errors.Is(err, store.ErrDuplicate) {
		name = *req.Name
	}
	if err != nil {
		server.teamErr(w, err, name)
		return
	}
	if req.Name != nil || team.ClaudeLogin != wasLogin {
		server.teamLoopsChanged(r, team, wasLogin)
	}
	server.writeTeam(w, r, 200, team)
}

// editTeam writes edit to the team name, putting it on the Claude login
// putOn when the edit names one, and returns the login the team named
// before. Reading that login and writing the new one sit under
// teamEditMu, so two concurrent edits can't both read the same login and
// leave a loop the second one moved unrefreshed.
func (server *Server) editTeam(ctx context.Context, name, putOn string, edit store.TeamEdit) (wasLogin string, team *store.Team, refusal *requestError, err error) {
	server.teamEditMu.Lock()
	defer server.teamEditMu.Unlock()
	before, err := server.Store.Teams().Get(ctx, name)
	if err != nil {
		return "", nil, nil, err
	}
	refusal, err = server.putOnLogin(ctx, putOn, func() (err error) {
		team, err = server.Store.Teams().Edit(ctx, name, edit)
		return err
	})
	return before.ClaudeLogin, team, refusal, err
}

// handleDeleteTeam deletes a team with no loops in it, and its channels
// with it, as a deleted channel goes. One that still has loops is refused,
// naming them: moving each out is the operator choosing where it goes
// (ADR-0066).
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

// teamLoopsChanged hands each of the team's loops its row as it now stands,
// as a loop's PATCH does: in it under a new name, or on its new Claude
// login. A loop the team's login moved, one that names none of its own,
// closes its process at its next quiet boundary, as a loop's own move does
// (ADR-0060, ADR-0066); wasLogin is the team's login before the edit.
func (server *Server) teamLoopsChanged(r *http.Request, team *store.Team, wasLogin string) {
	for _, loopID := range team.LoopIDs {
		updated, err := server.Store.Loops().Get(r.Context(), loopID)
		if err != nil {
			continue
		}
		server.Manager.UpdateLoop(updated)
		was := *updated
		was.TeamClaudeLogin = wasLogin
		if effectiveLogin(updated) == effectiveLogin(&was) {
			continue
		}
		server.loopChanged(r.Context(), updated.ID)
		if actor, ok := server.Manager.Get(updated.ID); ok {
			actor.RefreshEnv()
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
