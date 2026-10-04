package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
)

// Rooms (ADR-0038): the surface chats a loop's bot is in, each bound
// to one of the loop's channels or waiting for the operator to bind it. The
// hub records a room at its first message; the loop page binds it from a
// pick-list of the loop's channels, or by a pasted id.

const (
	codeRoomSurface   = "room_surface_unsupported"
	codeRoomIDInvalid = "room_id_invalid"
	codeRoomNotFound  = "room_not_found"
	codeRoomInUse     = "room_in_use"
	codeNotInChannel  = "not_in_channel"
	codeNoBot         = "no_bot"
)

// roomView is a room as the control room reads it. Channel "" is unbound.
type roomView struct {
	Surface     string `json:"surface"`
	RoomID      string `json:"room_id"`
	Title       string `json:"title"`
	Channel     string `json:"channel"`
	FirstSeenAt int64  `json:"first_seen_at"`
	BoundAt     int64  `json:"bound_at"`
}

func viewOfRoom(room *store.Room) roomView {
	return roomView{Surface: room.Surface, RoomID: room.RoomID, Title: room.Title, Channel: room.Channel,
		FirstSeenAt: room.FirstSeenAt, BoundAt: room.BoundAt}
}

func (server *Server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	rows, err := server.Store.Rooms().List(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]roomView, 0, len(rows))
	for _, room := range rows {
		views = append(views, viewOfRoom(room))
	}
	writeJSON(w, 200, views)
}

// handlePutRoom binds a room to one of the loop's channels. It is also the
// pasted-id fallback: a room the bot never heard from is added bound.
func (server *Server) handlePutRoom(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	var body struct {
		Surface string `json:"surface"`
		RoomID  string `json:"room_id"`
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	roomID, refused := roomIDOf(body.Surface, body.RoomID)
	if refused != nil {
		server.refuse(w, refused)
		return
	}
	channel, err := server.Store.Channels().Get(r.Context(), body.Channel)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 400, codeChannelNotFound, "channel %q not found", body.Channel)
		return
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if !slices.Contains(channel.LoopIDs, loopRecord.ID) {
		server.jsonErrCode(w, 409, codeNotInChannel, "%s is not in channel %q; add it there first", loopRecord.Name, channel.Name)
		return
	}
	if body.Surface == store.SurfaceTelegram && loopRecord.TGBotToken == "" {
		server.jsonErrCode(w, 409, codeNoBot, "%s has no Telegram bot to sit in the room", loopRecord.Name)
		return
	}
	if body.Surface == store.SurfaceSlack && loopRecord.SlackBotToken == "" {
		server.jsonErrCode(w, 409, codeNoBot, "%s has no Slack app to sit in the channel", loopRecord.Name)
		return
	}
	room, err := server.Store.Rooms().Bind(r.Context(), loopRecord.ID, body.Surface, roomID,
		channel.Name, time.Now().UnixMilli())
	if errors.Is(err, store.ErrRoomInUse) {
		server.jsonErrCode(w, 409, codeRoomInUse, "that room carries another channel for another loop; a room carries one channel")
		return
	}
	if err != nil {
		server.storeErr(w, err, "loop")
		return
	}
	server.roomsChanged(r, loopRecord.ID)
	writeJSON(w, 200, viewOfRoom(room))
}

// slackChannelID is a Slack channel's id as Slack spells it: C for a
// public channel, G for a private one an older workspace made. A DM's D id
// is no room.
var slackChannelID = regexp.MustCompile(`^[CG][A-Z0-9]{2,}$`)

// roomIDOf reads a room id as its surface spells it, so a pasted id and a
// heard one are the same row.
func roomIDOf(surface, raw string) (string, *requestError) {
	raw = strings.TrimSpace(raw)
	switch surface {
	case store.SurfaceTelegram:
		// a Telegram group's chat id is negative, spelled back in decimal
		chatID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || chatID >= 0 {
			return "", &requestError{status: http.StatusBadRequest, code: codeRoomIDInvalid,
				msg: "a Telegram group's id is a negative number, like -1001234567890"}
		}
		return strconv.FormatInt(chatID, 10), nil
	case store.SurfaceSlack:
		if !slackChannelID.MatchString(raw) {
			return "", &requestError{status: http.StatusBadRequest, code: codeRoomIDInvalid,
				msg: "a Slack channel's id starts with C, like C0123456789"}
		}
		return raw, nil
	}
	return "", &requestError{status: http.StatusBadRequest, code: codeRoomSurface,
		msg: "rooms are bound on telegram or slack"}
}

func (server *Server) handleDeleteRoom(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	err := server.Store.Rooms().Forget(r.Context(), loopRecord.ID, r.PathValue("surface"), r.PathValue("room"))
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 404, codeRoomNotFound, "%s has no room %s on %s", loopRecord.Name, r.PathValue("room"), r.PathValue("surface"))
		return
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.roomsChanged(r, loopRecord.ID)
	w.WriteHeader(http.StatusNoContent)
}

// roomsChanged tells the loop and the control room that the loop's rooms
// moved: the actor reads where its channels are carried, and the loop page
// refetches the list.
func (server *Server) roomsChanged(r *http.Request, loopID string) {
	updated, err := server.Store.Loops().Get(r.Context(), loopID)
	if err != nil {
		return // deleted under us: nobody is left to tell
	}
	server.Manager.UpdateLoop(updated)
	server.Bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopID, Payload: map[string]any{
		"loop_id": loopID, "name": updated.Name, "rooms_changed": true,
	}})
}
