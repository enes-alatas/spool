package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// Channels (ADR-0038): the hub's named conversations and the loops in each.
// The operator creates and describes them here and chooses who is in them;
// a loop's rooms (rooms.go) are where people read them.

const (
	codeChannelNameInvalid = "channel_name_invalid"
	codeChannelExists      = "channel_exists"
	codeChannelNotFound    = "channel_not_found"
	codeChannelReserved    = "channel_reserved"
	// A team's channels are kept to its loops (ADR-0066).
	codeChannelInOtherTeam  = "channel_in_other_team"
	codeChannelHasOutsiders = "channel_has_outsiders"
	codeChannelIsHome       = "channel_is_home"
)

// maxChannelDescription bounds a description in characters: one line the
// operator writes for the loops in it to read.
const maxChannelDescription = 280

// channelView is a channel as the control room reads it: its loops by name,
// sorted, since a loop's id is not something the operator knows it by.
type channelView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
	Loops       []string `json:"loops"`
	// Team is the team the channel belongs to, "" for a hub channel
	// (ADR-0066).
	Team string `json:"team"`
}

func (server *Server) channelViews(r *http.Request, channels ...*store.Channel) ([]channelView, error) {
	loops, err := server.Store.Loops().List(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(loops))
	for _, loopRecord := range loops {
		names[loopRecord.ID] = loopRecord.Name
	}
	views := make([]channelView, 0, len(channels))
	for _, channel := range channels {
		view := channelView{Name: channel.Name, Description: channel.Description, CreatedAt: channel.CreatedAt, Loops: []string{}, Team: channel.Team}
		for _, id := range channel.LoopIDs {
			if name, ok := names[id]; ok {
				view.Loops = append(view.Loops, name)
			}
		}
		sort.Strings(view.Loops)
		views = append(views, view)
	}
	return views, nil
}

func (server *Server) writeChannel(w http.ResponseWriter, r *http.Request, status int, channel *store.Channel) {
	views, err := server.channelViews(r, channel)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, status, views[0])
}

// channelErr answers a store error about the channel named in the path.
func (server *Server) channelErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeChannelNotFound, "channel %q not found", r.PathValue("name"))
	case errors.Is(err, store.ErrChannelIsHome):
		server.jsonErrCode(w, 409, codeChannelIsHome, "channel %q is its team's home; give the team another home channel, or none, first", r.PathValue("name"))
	default:
		server.jsonErr(w, 500, "%v", err)
	}
}

func (server *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := server.Store.Channels().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views, err := server.channelViews(r, channels...)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

func (server *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	server.writeChannel(w, r, 200, channel)
}

// handleChannelMessages is one channel's timeline, newest first. It answers by name whether or not the
// channel still exists: messages said in a deleted channel keep its name,
// and a channel created again under it continues that history (ADR-0038).
func (server *Server) handleChannelMessages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !store.ValidChannelName(name) {
		server.jsonErrCode(w, 400, codeChannelNameInvalid, "a channel name is 1 to 32 of a-z, 0-9 and '-', not starting with '-'")
		return
	}
	msgs, err := server.Store.Messages().ListChannel(r.Context(), name, queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	server.writeMessages(w, r, msgs)
}

// handleChannelPost is the operator posting into a channel from the control
// room, which is in every channel. Unlike a loop's composer, it names no
// loop implicitly: it wakes the loops its text addresses among the
// channel's, a post that addresses nobody is kept and wakes nobody, as a
// human's post in a mirrored room is, and it never leaves the hub
// (ADR-0032, ADR-0038). A reply addresses the author of what it answers,
// as a native reply does (ADR-0025). A channel that does not exist has no
// one to hear it.
func (server *Server) handleChannelPost(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := server.Store.Channels().Get(r.Context(), name); err != nil {
		server.channelErr(w, r, err)
		return
	}
	var req postChannelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		server.jsonErr(w, 400, "non-empty text required")
		return
	}
	if req.ReplyToID != 0 && !server.channelReplyTarget(r.Context(), w, req.ReplyToID, name) {
		return
	}
	err := server.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:       store.OriginWeb,
		Author:       authorOf(r, req.Author),
		Text:         req.Text,
		Conversation: store.ConversationGroup,
		Channel:      name,
		ReplyToID:    req.ReplyToID,
		UploadID:     req.AttachmentID,
		UploadSender: uploadSenderOf(callerOf(r)),
	})
	if err != nil {
		server.ingestErr(w, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

func (server *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		// Team makes the channel that team's (ADR-0066); "" a hub channel.
		Team string `json:"team"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	channel := &store.Channel{Name: req.Name, Description: strings.TrimSpace(req.Description), CreatedAt: time.Now().UnixMilli(), Team: req.Team}
	switch {
	case channel.Name == store.RetiredChannel:
		server.jsonErrCode(w, 400, codeChannelReserved, "%q was the fleet channel's name, and no channel takes it again", store.RetiredChannel)
		return
	case !store.ValidChannelName(channel.Name):
		server.jsonErrCode(w, 400, codeChannelNameInvalid, "a channel name is 1 to 32 of a-z, 0-9 and '-', not starting with '-'")
		return
	case !server.validDescription(w, channel.Description):
		return
	}
	if err := server.Store.Channels().Create(r.Context(), channel); err != nil {
		switch {
		case errors.Is(err, store.ErrDuplicate):
			server.jsonErrCode(w, 409, codeChannelExists, "a channel named %q already exists", channel.Name)
		case errors.Is(err, store.ErrNotFound):
			server.jsonErrCode(w, 404, codeTeamNotFound, "team %q not found", channel.Team)
		default:
			server.jsonErr(w, 500, "%v", err)
		}
		return
	}
	server.writeChannel(w, r, 201, channel)
}

// handlePatchChannel rewrites a channel's description, or gives it to a
// team or back to the hub (ADR-0066). A channel goes to a team only whole:
// while it holds a loop outside the team, the move is refused naming them.
func (server *Server) handlePatchChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description *string `json:"description"`
		Team        *string `json:"team"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Description == nil && req.Team == nil {
		server.jsonErr(w, 400, "nothing to change: a channel's description and team are the fields that change")
		return
	}
	name := r.PathValue("name")
	var description string
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
		if !server.validDescription(w, description) {
			return
		}
	}
	channel, err := server.Store.Channels().Get(r.Context(), name)
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	// the team first: it is the half that can be refused
	if req.Team != nil && *req.Team != channel.Team {
		channel, err = server.Store.Channels().SetTeam(r.Context(), name, *req.Team)
		switch {
		case errors.Is(err, store.ErrChannelHasOutsiders):
			server.channelHasOutsiders(w, r, name, *req.Team)
			return
		case errors.Is(err, store.ErrNotFound):
			server.jsonErrCode(w, 404, codeTeamNotFound, "team %q not found", *req.Team)
			return
		case err != nil:
			server.channelErr(w, r, err)
			return
		}
	}
	if req.Description != nil {
		if channel, err = server.Store.Channels().SetDescription(r.Context(), name, description); err != nil {
			server.channelErr(w, r, err)
			return
		}
	}
	server.writeChannel(w, r, 200, channel)
}

// channelHasOutsiders refuses giving a channel to a team, naming the loops
// in it that are not the team's.
func (server *Server) channelHasOutsiders(w http.ResponseWriter, r *http.Request, name, team string) {
	channel, err := server.Store.Channels().Get(r.Context(), name)
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	loops, err := server.Store.Loops().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	in := map[string]bool{}
	for _, id := range channel.LoopIDs {
		in[id] = true
	}
	var outsiders []string
	for _, loopRecord := range loops {
		if in[loopRecord.ID] && loopRecord.Team != team {
			outsiders = append(outsiders, loopRecord.Name)
		}
	}
	sort.Strings(outsiders)
	server.jsonErrCode(w, 409, codeChannelHasOutsiders, "channel %q holds loops outside team %q: %s; take them out first",
		name, team, strings.Join(outsiders, ", "))
}

func (server *Server) validDescription(w http.ResponseWriter, description string) bool {
	if utf8.RuneCountInString(description) > maxChannelDescription {
		server.jsonErr(w, 400, "a channel description is at most %d characters", maxChannelDescription)
		return false
	}
	if strings.ContainsAny(description, "\r\n") {
		server.jsonErr(w, 400, "a channel description is one line")
		return false
	}
	return true
}

func (server *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	if err := server.Store.Channels().Delete(r.Context(), channel.Name); err != nil {
		server.channelErr(w, r, err)
		return
	}
	// its rooms were unbound with it
	for _, loopID := range channel.LoopIDs {
		server.roomsChanged(r, loopID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChannelLoop puts a loop in a channel (in) or takes it out.
func (server *Server) handleChannelLoop(in bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelName := r.PathValue("name")
		channel, err := server.Store.Channels().Get(r.Context(), channelName)
		if err != nil {
			server.channelErr(w, r, err)
			return
		}
		loopRecord, err := server.Store.Loops().GetByName(r.Context(), r.PathValue("loop"))
		if err != nil {
			server.storeErr(w, err, "loop")
			return
		}
		write := server.Store.Channels().RemoveLoop
		if in {
			write = server.Store.Channels().AddLoop
		}
		if err := write(r.Context(), channelName, loopRecord.ID, time.Now().UnixMilli()); err != nil {
			if errors.Is(err, store.ErrChannelInOtherTeam) {
				server.jsonErrCode(w, 409, codeChannelInOtherTeam, "channel %q is team %q's, and loop %q isn't in that team",
					channelName, channel.Team, loopRecord.Name)
				return
			}
			server.channelErr(w, r, err)
			return
		}
		if !in {
			// its room for the channel was unbound with it
			server.roomsChanged(r, loopRecord.ID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
