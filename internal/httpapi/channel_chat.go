package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A channel's chat (ADR-0070): the one Telegram group or Slack channel a
// channel is bound to, on the channel's page, once for every member loop
// whose bot is in it. A room (rooms.go) is that chat as one loop's bot
// knows it.

const (
	codeChatNotFound         = "chat_not_found"
	codeChannelHasChat       = "channel_has_chat"
	codeLoopNotInChannel     = "loop_not_in_channel"
	codeNotInChat            = "not_in_chat"
	codeLoopNotOnSurface     = "loop_not_on_surface"
	codeMembersNotOnSurface  = "members_not_on_surface"
	chatStatusCarrying       = "carrying"
	chatStatusNotInChat      = "not_in_chat"
	chatStatusNoBot          = "no_bot"
	chatStatusUnbound        = "unbound"
	telegramStartGroupPrefix = "https://t.me/"
)

// chatView is a channel's chat as the channel's page reads it.
type chatView struct {
	Surface string `json:"surface"`
	ChatID  string `json:"chat_id"`
	Title   string `json:"title"`
	BoundAt int64  `json:"bound_at"`
	// SeenAt is when a member's bot was first in the chat, null for a
	// pasted chat none has been in: the page shows it as not seen yet.
	SeenAt  *int64           `json:"seen_at"`
	Members []chatMemberView `json:"members"`
}

// chatMemberView is one member loop's place in the channel's chat.
type chatMemberView struct {
	Loop   string `json:"loop"`
	Status string `json:"status"`
	// AddLink adds a not_in_chat member's Telegram bot to a group.
	AddLink string `json:"add_link,omitempty"`
}

// botOn reports whether the loop has a bot on surface.
func botOn(loopRecord *store.Loop, surface string) bool {
	switch surface {
	case store.SurfaceTelegram:
		return loopRecord.TGBotToken != ""
	case store.SurfaceSlack:
		return loopRecord.SlackBotToken != ""
	}
	return false
}

// startGroupLink is the link that adds a Telegram bot to a group of the
// person's choosing, "" for a bot whose name the hub doesn't know.
func startGroupLink(loopRecord *store.Loop) string {
	if loopRecord.TGBotUsername == "" {
		return ""
	}
	return telegramStartGroupPrefix + url.PathEscape(loopRecord.TGBotUsername) + "?startgroup=spool"
}

// channelChat is the chat part of a channel's view: its chat, null for
// none, and the surfaces every member has a bot on, which are the chats
// the page may offer to add.
func (server *Server) channelChat(ctx context.Context, channel *store.Channel) (*chatView, []string, error) {
	members := make([]*store.Loop, 0, len(channel.LoopIDs))
	for _, loopID := range channel.LoopIDs {
		loopRecord, err := server.Store.Loops().Get(ctx, loopID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		members = append(members, loopRecord)
	}
	sort.Slice(members, func(first, second int) bool { return members[first].Name < members[second].Name })
	surfaces := []string{}
	for _, surface := range []string{store.SurfaceSlack, store.SurfaceTelegram} {
		every := true
		for _, member := range members {
			every = every && botOn(member, surface)
		}
		if every {
			surfaces = append(surfaces, surface)
		}
	}
	chat, err := server.Store.Channels().Chat(ctx, channel.Name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, surfaces, nil
	}
	if err != nil {
		return nil, nil, err
	}
	rooms, err := server.Store.Rooms().ListByRoom(ctx, chat.Surface, chat.ChatID)
	if err != nil {
		return nil, nil, err
	}
	roomOf := make(map[string]*store.Room, len(rooms))
	for _, room := range rooms {
		roomOf[room.LoopID] = room
	}
	view := &chatView{Surface: chat.Surface, ChatID: chat.ChatID, Title: chat.Title, BoundAt: chat.BoundAt,
		Members: make([]chatMemberView, 0, len(members))}
	if chat.SeenAt != 0 {
		seenAt := chat.SeenAt
		view.SeenAt = &seenAt
	}
	for _, member := range members {
		place := chatMemberView{Loop: member.Name}
		room := roomOf[member.ID]
		switch {
		case !botOn(member, chat.Surface):
			place.Status = chatStatusNoBot
		case room == nil:
			place.Status = chatStatusNotInChat
			if chat.Surface == store.SurfaceTelegram {
				place.AddLink = startGroupLink(member)
			}
		case room.Channel == channel.Name:
			place.Status = chatStatusCarrying
		default:
			place.Status = chatStatusUnbound
		}
		view.Members = append(view.Members, place)
	}
	return view, surfaces, nil
}

// foundChatView is a chat the channel's page offers to confirm.
type foundChatView struct {
	Surface     string   `json:"surface"`
	ChatID      string   `json:"chat_id"`
	Title       string   `json:"title"`
	FirstSeenAt int64    `json:"first_seen_at"`
	FoundBy     []string `json:"found_by"`
}

func (server *Server) handleFoundChats(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	found, err := server.Store.Channels().FoundChats(r.Context(), channel.Name)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]foundChatView, 0, len(found))
	for _, chat := range found {
		names, err := server.loopNamesOf(r.Context(), chat.FoundBy)
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		views = append(views, foundChatView{Surface: chat.Surface, ChatID: chat.ChatID, Title: chat.Title,
			FirstSeenAt: chat.FirstSeenAt, FoundBy: names})
	}
	writeJSON(w, 200, views)
}

// handlePutChat binds a chat to the channel: one the page found, or a
// pasted id no member's bot is in yet.
func (server *Server) handlePutChat(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	var body struct {
		Surface string `json:"surface"`
		ChatID  string `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	chatID, refused := roomIDOf(body.Surface, body.ChatID)
	if refused != nil {
		server.refuse(w, refused)
		return
	}
	_, err = server.Store.Channels().BindChat(r.Context(), &store.ChannelChat{Channel: channel.Name,
		Surface: body.Surface, ChatID: chatID, BoundAt: time.Now().UnixMilli(), BoundBy: authorOf(r, "")})
	if refusal := server.chatRefusal(r.Context(), err, channel.Name); refusal != nil {
		server.refuse(w, refusal)
		return
	}
	server.membersRoomsChanged(r, channel.LoopIDs)
	server.writeChat(w, r, channel.Name)
}

// chatRefusal is the answer to a store error binding a chat to channel,
// nil for none.
func (server *Server) chatRefusal(ctx context.Context, err error, channel string) *requestError {
	var offSurface *store.NotOnSurfaceError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return refusalf(404, codeChannelNotFound, "channel %q not found", channel)
	case errors.Is(err, store.ErrChannelHasChat):
		return refusalf(409, codeChannelHasChat, "channel %q has a chat already; unbind it first, since a channel has one chat", channel)
	case errors.Is(err, store.ErrRoomInUse):
		return refusalf(409, codeRoomInUse, "that chat is another channel's; a chat carries one channel")
	case errors.As(err, &offSurface):
		names, nameErr := server.loopNamesOf(ctx, offSurface.LoopIDs)
		if nameErr != nil {
			return refusalf(500, "", "%v", nameErr)
		}
		return refusalf(409, codeMembersNotOnSurface, "every loop in channel %q needs a bot on %s for it to have a chat there; %s has none",
			channel, offSurface.Surface, strings.Join(names, ", "))
	}
	return refusalf(500, "", "%v", err)
}

// writeChat answers a write with the channel's chat as its view shows it.
func (server *Server) writeChat(w http.ResponseWriter, r *http.Request, name string) {
	channel, err := server.Store.Channels().Get(r.Context(), name)
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	chat, _, err := server.channelChat(r.Context(), channel)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, chat)
}

func (server *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	err = server.Store.Channels().UnbindChat(r.Context(), channel.Name)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 404, codeChatNotFound, "channel %q has no chat", channel.Name)
		return
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.membersRoomsChanged(r, channel.LoopIDs)
	w.WriteHeader(http.StatusNoContent)
}

// handlePutChatMember binds a member's room for the channel's chat, which
// only one the upgrade to channel chats left unbound needs.
func (server *Server) handlePutChatMember(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	loopRecord, err := server.Store.Loops().GetByName(r.Context(), r.PathValue("loop"))
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 404, codeLoopNotInChannel, "loop %q is not in channel %q", r.PathValue("loop"), channel.Name)
		return
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	err = server.Store.Channels().BindMember(r.Context(), channel.Name, loopRecord.ID, time.Now().UnixMilli())
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeChatNotFound, "channel %q has no chat", channel.Name)
		return
	case errors.Is(err, store.ErrNotInChannel):
		server.jsonErrCode(w, 404, codeLoopNotInChannel, "loop %q is not in channel %q", loopRecord.Name, channel.Name)
		return
	case errors.Is(err, store.ErrNotInChat):
		server.jsonErrCode(w, 409, codeNotInChat, "%s's bot is not in channel %q's chat; add it there first", loopRecord.Name, channel.Name)
		return
	case err != nil:
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.roomsChanged(r, loopRecord.ID)
	w.WriteHeader(http.StatusNoContent)
}

// loopNotOnSurface is the answer to a write that would put a loop with no
// bot on a channel's chat surface among the channel's members, nil when
// err is no such refusal.
func (server *Server) loopNotOnSurface(ctx context.Context, err error) *requestError {
	var offSurface *store.NotOnSurfaceError
	if !errors.As(err, &offSurface) {
		return nil
	}
	names, nameErr := server.loopNamesOf(ctx, offSurface.LoopIDs)
	if nameErr != nil {
		return refusalf(500, "", "%v", nameErr)
	}
	return refusalf(409, codeLoopNotOnSurface, "a channel with a chat on %s takes only loops with a bot there; %s has none",
		offSurface.Surface, strings.Join(names, ", "))
}

// loopNamesOf are the names of loopIDs, sorted, a gone loop by its id.
func (server *Server) loopNamesOf(ctx context.Context, loopIDs []string) ([]string, error) {
	names := make([]string, 0, len(loopIDs))
	for _, loopID := range loopIDs {
		loopRecord, err := server.Store.Loops().Get(ctx, loopID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			names = append(names, loopID)
		case err != nil:
			return nil, err
		default:
			names = append(names, loopRecord.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// membersRoomsChanged tells each loop and the control room that the
// loops' rooms moved, as a channel's chat bound or unbound moves them.
func (server *Server) membersRoomsChanged(r *http.Request, loopIDs []string) {
	for _, loopID := range loopIDs {
		server.roomsChanged(r, loopID)
	}
}

// channelHasChat reports whether the channel has a chat. A read that fails
// says it has one, so the caller tells the loop rather than leaves it
// unsure where its channel is carried.
func (server *Server) channelHasChat(r *http.Request, channel string) bool {
	_, err := server.Store.Channels().Chat(r.Context(), channel)
	return !errors.Is(err, store.ErrNotFound)
}
