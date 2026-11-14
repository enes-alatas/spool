package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
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
	codeAlreadyInChat        = "already_in_chat"
	codeJoinNeedsScope       = "needs_scope"
	codeChatPrivate          = "chat_private"
	codeJoinUnsupported      = "join_unsupported"
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
	// Join says whether a not_in_chat member's Slack app can join the chat
	// by itself, on the operator's click: available, needs_scope or
	// private. It is left out when Slack didn't answer.
	Join surface.JoinOffer `json:"join,omitempty"`
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
	server.fillJoinOffers(ctx, view, members, chat)
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

// joinWait bounds the Slack calls a channel's page waits on to offer its
// members' Join buttons, all of them together: a slow answer leaves a
// button out rather than the page waiting.
var joinWait = 5 * time.Second

// joinOffer is whether member's bot can join chat by itself, "" on a
// surface with no such call or when the platform didn't answer.
func (server *Server) joinOffer(ctx context.Context, member *store.Loop, chat *store.ChannelChat) surface.JoinOffer {
	joiner, ok := server.Surfaces[chat.Surface].(surface.ChatJoiner)
	if !ok {
		return ""
	}
	offer, err := joiner.JoinOffer(ctx, member.ID, chat.ChatID)
	if err != nil {
		server.Log.Warn("ask whether a loop's bot can join a channel's chat", "loop", member.Name, "chat", chat.ChatID, "err", err)
		return ""
	}
	return offer
}

// handleJoinChat has a member loop's bot join the channel's chat by
// itself, on the operator's click and only then (ADR-0070). The room it
// records binds through the channel's chat, as any arrival's does.
func (server *Server) handleJoinChat(w http.ResponseWriter, r *http.Request) {
	channel, err := server.Store.Channels().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.channelErr(w, r, err)
		return
	}
	chat, err := server.Store.Channels().Chat(r.Context(), channel.Name)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 404, codeChatNotFound, "channel %q has no chat", channel.Name)
		return
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	loopRecord, err := server.Store.Loops().GetByName(r.Context(), r.PathValue("loop"))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if loopRecord == nil || !slices.Contains(channel.LoopIDs, loopRecord.ID) {
		server.jsonErrCode(w, 404, codeLoopNotInChannel, "loop %q is not in channel %q", r.PathValue("loop"), channel.Name)
		return
	}
	if !botOn(loopRecord, chat.Surface) {
		server.jsonErrCode(w, 409, codeLoopNotOnSurface, "%s has no bot on %s", loopRecord.Name, chat.Surface)
		return
	}
	joiner, ok := server.Surfaces[chat.Surface].(surface.ChatJoiner)
	if !ok {
		server.jsonErrCode(w, 409, codeJoinUnsupported, "a bot can't join a %s chat by itself; add it there instead", chat.Surface)
		return
	}
	rooms, err := server.Store.Rooms().ListByRoom(r.Context(), chat.Surface, chat.ChatID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	for _, room := range rooms {
		if room.LoopID == loopRecord.ID {
			server.jsonErrCode(w, 409, codeAlreadyInChat, "%s's bot is in channel %q's chat already", loopRecord.Name, channel.Name)
			return
		}
	}
	err = joiner.JoinChat(r.Context(), loopRecord.ID, chat.ChatID)
	switch {
	case errors.Is(err, surface.ErrJoinNeedsScope):
		server.jsonErrCode(w, 409, codeJoinNeedsScope, "%s's Slack app needs the channels:join scope to join by itself; invite it instead, or add the scope", loopRecord.Name)
		return
	case errors.Is(err, surface.ErrJoinPrivate):
		server.jsonErrCode(w, 409, codeChatPrivate, "channel %q's chat is private, so only an invite brings %s's app in", channel.Name, loopRecord.Name)
		return
	case err != nil:
		server.jsonErr(w, http.StatusBadGateway, "%s's bot couldn't join the chat: %v", loopRecord.Name, err)
		return
	}
	server.roomsChanged(r, loopRecord.ID)
	w.WriteHeader(http.StatusNoContent)
}

// fillJoinOffers sets join on each not_in_chat member of a chat whose
// surface can join one by itself. The offers are asked at once, under one
// joinWait, so a slow platform holds a channel's view, and every write
// that answers with it, joinWait in all whatever the member count.
func (server *Server) fillJoinOffers(ctx context.Context, view *chatView, members []*store.Loop, chat *store.ChannelChat) {
	if _, ok := server.Surfaces[chat.Surface].(surface.ChatJoiner); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, joinWait)
	defer cancel()
	var asking sync.WaitGroup
	for index := range view.Members {
		if view.Members[index].Status != chatStatusNotInChat {
			continue
		}
		asking.Add(1)
		go func(place *chatMemberView, member *store.Loop) {
			defer asking.Done()
			place.Join = server.joinOffer(ctx, member, chat)
		}(&view.Members[index], members[index])
	}
	asking.Wait()
}
