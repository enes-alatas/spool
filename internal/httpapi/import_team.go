package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/promptschat"
	"github.com/enes-alatas/spool/internal/store"
)

// createTeamReq is a team the operator read in its draft and chose to
// create, as edited: its name, its home channel and description, its
// loops, and the runtime every loop runs on, empty for the hub's default.
type createTeamReq struct {
	// Team names the team, the channel's name when empty.
	Team        string           `json:"team"`
	Channel     string           `json:"channel"`
	Description string           `json:"description"`
	Runtime     string           `json:"runtime"`
	Loops       []createTeamLoop `json:"loops"`
}

// createTeamLoop is one loop of a team to create.
type createTeamLoop struct {
	Name    string `json:"name"`
	Mission string `json:"mission"`
	// Skills are the ones the operator ticked, as the draft carried them
	// (#1364).
	Skills []promptschat.Skill `json:"skills"`
}

// maxTeamLoops bounds the loops one team create makes, as a team import
// bounds the bots it reads.
const maxTeamLoops = 12

// createdTeam answers a team create: the team it made, its home channel
// and its loops.
type createdTeam struct {
	Team    string      `json:"team"`
	Channel string      `json:"channel"`
	Loops   []*loopView `json:"loops"`
}

// handleImportPromptsChatTeam answers the draft of the prompts.chat team
// at ?url= (ADR-0063): the loops it would make, one per member named from
// its role, each mission ending with its channel and handoffs, and the
// channel holding them. Names taken on the hub are suffixed. It creates
// nothing.
func (server *Server) handleImportPromptsChatTeam(w http.ResponseWriter, r *http.Request) {
	if server.PromptsChat == nil {
		server.jsonErrCode(w, http.StatusServiceUnavailable, codeImportOff, "this hub doesn't import from %s", promptschat.Host)
		return
	}
	loopTaken, channelTaken, err := server.namesTaken(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	team, err := promptschat.ImportTeam(r.Context(), server.PromptsChat, r.URL.Query().Get("url"), loopTaken, channelTaken)
	var refusal *promptschat.Refusal
	switch {
	case errors.As(err, &refusal):
		server.jsonErrCode(w, http.StatusBadRequest, codeImportRefused, "%s", refusal.Reason)
		return
	case err != nil:
		server.Log.Warn("prompts.chat team import", "err", err)
		server.jsonErrCode(w, http.StatusBadGateway, codeImportFailed, "%s didn't answer; try again in a minute", promptschat.Host)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

// handleImportTeam creates a team on the hub from the draft the operator
// read (ADR-0063): the team, its home channel and its loops, each loop in
// the team and so in the channel. It fetches nothing. It is all or
// nothing: every loop is checked before any is stored, and none starts
// until the team and all of them are stored, so a failure on the way
// removes what it stored and answers why.
func (server *Server) handleImportTeam(w http.ResponseWriter, r *http.Request) {
	var req createTeamReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	switch {
	case req.Runtime == store.RuntimeCloud:
		// a cloud loop needs a repo, branch and environment of its own,
		// which a team create has none of
		server.jsonErr(w, 400, "a team's loops run on bare or docker; make a cloud loop one at a time")
		return
	case len(req.Loops) == 0 || len(req.Loops) > maxTeamLoops:
		server.jsonErr(w, 400, "a team has 1 to %d loops", maxTeamLoops)
		return
	case req.Channel == store.RetiredChannel:
		server.jsonErrCode(w, 400, codeChannelReserved, "%q was the fleet channel's name, and no channel takes it again", store.RetiredChannel)
		return
	case !store.ValidChannelName(req.Channel):
		server.jsonErrCode(w, 400, codeChannelNameInvalid, "a channel name is 1 to 32 of a-z, 0-9 and '-', not starting with '-'")
		return
	case teamNameRefusal(teamName(req)) != nil:
		server.refuse(w, teamNameRefusal(teamName(req)))
		return
	case !server.validDescription(w, req.Description):
		return
	}
	var loops []*store.Loop
	named := map[string]bool{}
	for _, member := range req.Loops {
		loopRecord, refusal := server.prepareLoop(r.Context(), &createLoopReq{
			Name: member.Name, Mission: member.Mission, Runtime: req.Runtime, Skills: member.Skills,
		})
		if refusal != nil {
			server.refuse(w, refusal)
			return
		}
		if named[loopRecord.Name] {
			server.jsonErr(w, 400, "the team names the loop %q twice", loopRecord.Name)
			return
		}
		named[loopRecord.Name] = true
		loops = append(loops, loopRecord)
	}
	unstage := func() {
		for _, loopRecord := range loops {
			server.unstageSkills(loopRecord)
		}
	}
	for index, loopRecord := range loops {
		if err := server.stageSkills(loopRecord, req.Loops[index].Skills); err != nil {
			unstage()
			server.jsonErr(w, 500, "skills not staged: %v", err)
			return
		}
	}
	team := &store.Team{Name: teamName(req), Description: req.Description, HomeChannel: req.Channel}
	if failed := server.storeTeam(r.Context(), team, loops); failed != nil {
		unstage()
		server.refuse(w, failed)
		return
	}
	// each loop is minted with a hub MCP token
	server.secretsChanged(r.Context())
	answer := createdTeam{Team: team.Name, Channel: team.HomeChannel}
	for _, loopRecord := range loops {
		server.startLoop(r, loopRecord)
		answer.Loops = append(answer.Loops, server.view(r.Context(), loopRecord))
	}
	writeJSON(w, http.StatusCreated, answer)
}

// namesTaken says whether a loop name and a channel name are in use on
// the hub. A loop name is in use as namesInUse says, @all's among them,
// so a draft suffixes what its create would refuse. A channel name is in
// use as a channel's or a team's, since the create names its team as its
// channel unless told otherwise.
func (server *Server) namesTaken(ctx context.Context) (loopTaken, channelTaken func(string) bool, err error) {
	names, err := server.namesInUse(ctx)
	if err != nil {
		return nil, nil, err
	}
	channels, err := server.Store.Channels().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	channelNames := map[string]bool{store.RetiredChannel: true}
	for _, channel := range channels {
		channelNames[channel.Name] = true
	}
	teams, err := server.Store.Teams().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, team := range teams {
		channelNames[team.Name] = true
	}
	return names.taken, func(name string) bool { return channelNames[name] }, nil
}

// storeTeam stores team, with its home channel, then each loop in it, so
// every loop joins the home channel as a loop put in a team does
// (ADR-0066). A write that fails removes what was stored before it, and
// the refusal says why; a removal that fails too is named in it, for the
// operator to delete.
func (server *Server) storeTeam(ctx context.Context, team *store.Team, loops []*store.Loop) *requestError {
	team.CreatedAt = time.Now().UnixMilli()
	err := server.Store.Teams().Create(ctx, team)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		return refusalf(409, codeTeamExists, "a team named %q already exists", team.Name)
	case errors.Is(err, store.ErrChannelTaken):
		return refusalf(409, codeChannelExists, "a channel named %q already exists", team.HomeChannel)
	case err != nil:
		return refusalf(500, "", "%v", err)
	}
	var stored []*store.Loop
	undo := func(refusal *requestError) *requestError {
		var left []string
		for _, loopRecord := range stored {
			if err := server.Store.Loops().Delete(context.WithoutCancel(ctx), loopRecord.ID, time.Now().UnixMilli()); err != nil {
				server.Log.Error("team create: remove a loop it stored", "loop", loopRecord.Name, "err", err)
				left = append(left, "the loop "+loopRecord.Name)
			}
		}
		if len(left) == 0 {
			if err := server.Store.Teams().Delete(context.WithoutCancel(ctx), team.Name); err != nil {
				server.Log.Error("team create: remove the team it stored", "team", team.Name, "err", err)
				left = append(left, "the team "+team.Name)
			}
		} else {
			// a team still holding a loop can't be deleted
			left = append(left, "the team "+team.Name)
		}
		if len(left) > 0 {
			refusal.msg += "; " + strings.Join(left, ", ") + " could not be removed again: delete them"
		}
		return refusal
	}
	// the home channel carries the description too, as the channel an
	// import made before teams did: it is what the loops read of it
	if team.Description != "" {
		if _, err := server.Store.Channels().SetDescription(ctx, team.HomeChannel, team.Description); err != nil {
			return undo(refusalf(500, "", "%v", err))
		}
	}
	for _, loopRecord := range loops {
		loopRecord.Team = team.Name
		if refusal := server.storeLoop(ctx, loopRecord); refusal != nil {
			return undo(refusal)
		}
		stored = append(stored, loopRecord)
	}
	return nil
}

// teamName is the name of the team req makes: the one given, or its
// channel's.
func teamName(req createTeamReq) string {
	if name := strings.TrimSpace(req.Team); name != "" {
		return name
	}
	return req.Channel
}
