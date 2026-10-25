package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// privateConversationView is one of a loop's private conversations as its
// page lists them (#720): owner_dm, or dm:<name> under the person's name of
// now; the person's id on their surface, which outlives a rename; the
// Spool user they are linked to, left out for a person no user is linked
// as (#1111); and when the newest message in it was said.
type privateConversationView struct {
	Conversation string `json:"conversation"`
	Name         string `json:"name"`
	PersonID     string `json:"person_id"`
	User         string `json:"user,omitempty"`
	LastAt       int64  `json:"last_at"`
	// person is who it is with, as the messages record them.
	person string
}

// privateConversations is the loop's private conversations reader reads,
// newest first: a member's own, through their links, and every one for an
// admin (ADR-0050). The owner_dm can hold several owners' messages, and is
// listed once, under the person of its newest message the reader reads.
func (server *Server) privateConversations(ctx context.Context, loopID string, reader *privateReader) ([]privateConversationView, error) {
	threads, err := server.Store.Messages().ListPrivateThreads(ctx, loopID)
	if err != nil {
		return nil, err
	}
	linked, err := server.Store.UserLinks().LinkedUsers(ctx)
	if err != nil {
		return nil, err
	}
	views := []privateConversationView{}
	listedOwnerDM := false
	for _, thread := range threads {
		if !reader.all && !reader.persons[thread.DMPerson] {
			continue
		}
		if thread.Conversation == store.ConversationOwnerDM {
			if listedOwnerDM {
				continue
			}
			listedOwnerDM = true
		}
		view := privateConversationView{Conversation: thread.Conversation, LastAt: thread.LastAt, person: thread.DMPerson}
		if thread.DMPerson != "" {
			var username string
			view.PersonID, username, _ = server.senderOf(ctx, thread.DMPerson)
			view.Name = store.DMName(username, view.PersonID)
			view.User = linked[thread.DMPerson]
		}
		if store.IsDM(thread.Conversation) {
			view.Conversation = store.DMDestinationPrefix + view.Name
		}
		views = append(views, view)
	}
	return views, nil
}

// senderOf is person's id on their surface, and how the surface last named
// them; the names are empty for a sender the hub has no record of.
func (server *Server) senderOf(ctx context.Context, person string) (id, username, display string) {
	switch store.PersonSurface(person) {
	case store.SurfaceTelegram:
		id = strings.TrimPrefix(person, "tg:")
		if tgUserID, err := strconv.ParseInt(id, 10, 64); err == nil {
			if sender, err := server.Store.TGSenders().Get(ctx, tgUserID); err == nil {
				username, display = sender.Username, sender.Display
			}
		}
	case store.SurfaceSlack:
		id = strings.TrimPrefix(person, "slack:")
		if sender, err := server.Store.SlackSenders().Get(ctx, id); err == nil {
			username, display = sender.Username, sender.Display
		}
	}
	return id, username, display
}

// handleLoopConversations lists the loop's private conversations the
// caller reads, newest first.
func (server *Server) handleLoopConversations(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	reader, err := server.privateReaderOf(r.Context(), callerOf(r))
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views, err := server.privateConversations(r.Context(), loopRecord.ID, reader)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handleLoopDM answers a loop's dm:<name> thread: the person the name
// lists, among the private conversations the caller reads, by every name
// they had. A name the caller reads no conversation under answers an empty
// thread, as a conversation they may not read does, so the answer tells
// them nothing about who else the loop talks to. Should two people answer
// to one name, as a Telegram and a Slack handle can after the loop moves
// surface, the one it spoke with last is read.
func (server *Server) handleLoopDM(w http.ResponseWriter, r *http.Request, loopRecord *store.Loop, conversation string) {
	reader, err := server.privateReaderOf(r.Context(), callerOf(r))
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views, err := server.privateConversations(r.Context(), loopRecord.ID, reader)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	// A leading @ is let through, as a send lets it through.
	want := store.DMDestinationPrefix + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(conversation, store.DMDestinationPrefix), "@"))
	msgs := []*store.Message{}
	for _, view := range views {
		if view.Conversation == want {
			if msgs, err = server.Store.Messages().ListDM(r.Context(), loopRecord.ID, view.person, queryInt(r, "limit", 100)); err != nil {
				server.jsonErr(w, http.StatusInternalServerError, "%v", err)
				return
			}
			break
		}
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	server.writeMessages(w, r, msgs)
}
