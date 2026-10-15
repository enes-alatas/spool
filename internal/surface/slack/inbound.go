package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// Inbound: what a loop's app hears over Socket Mode becomes a hub message,
// a correction of one, or a reaction on one. The app subscribes to
// message.im, message.channels and message.groups, and to reaction_added
// and reaction_removed (the manifest the control room hands out), so every
// event that matters is a message, in the owner's DM or in a channel, an
// edit or deletion of one, or a reaction to one. A click on a poll's
// button is an interactive envelope instead (poll.go).

// eventCallback is an events_api envelope's payload, reduced to what
// ingest reads.
type eventCallback struct {
	TeamID string       `json:"team_id"`
	Event  messageEvent `json:"event"`
}

type messageEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// ChannelType is "im" for a DM with the app, "channel" or "group" for
	// a public or private channel, "mpim" for a group DM.
	ChannelType string `json:"channel_type"`
	Channel     string `json:"channel"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	// ThreadTS is the ts of the thread's first message, on every message
	// in a thread, that first one included.
	ThreadTS string `json:"thread_ts"`
	// Files are what a file_share message carries (#123).
	Files []File `json:"files"`
	// Reaction is the name of the emoji a reaction event adds or removes,
	// and Item what it is on.
	Reaction string `json:"reaction"`
	Item     struct {
		Type    string `json:"type"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	} `json:"item"`
	// Message is the message as it now reads, on a message_changed event,
	// and PreviousMessage how it read before, on that and on a
	// message_deleted, whose DeletedTS is the ts of the message deleted
	// (ADR-0055).
	Message         *changedMessage `json:"message"`
	PreviousMessage *changedMessage `json:"previous_message"`
	DeletedTS       string          `json:"deleted_ts"`
}

// changedMessage is a message as a message_changed or message_deleted
// event carries it. Edited is set only when a person changed its words:
// Slack sends message_changed when a link in it unfurls, too.
type changedMessage struct {
	User   string `json:"user"`
	BotID  string `json:"bot_id"`
	Text   string `json:"text"`
	TS     string `json:"ts"`
	Edited *struct {
		TS string `json:"ts"`
	} `json:"edited"`
}

// events is how many envelopes a link holds for ingest while it keeps
// reading. Ingest can call users.info, and a read loop that waited on it
// would leave the ping's answer unread.
const events = 256

// ingestLoop hands a link's envelopes on one at a time, in the order Slack
// sent them, until the link closes the channel: an event to ingest, and a
// click to ingestInteraction. What is left once the link is stopping is
// drained, not ingested.
func (adapter *Adapter) ingestLoop(ctx context.Context, link *link, envelopes <-chan frame) {
	for envelope := range envelopes {
		if ctx.Err() != nil {
			continue
		}
		if envelope.Type == "interactive" {
			adapter.ingestInteraction(ctx, link, envelope.Payload)
		} else {
			adapter.ingest(ctx, link, envelope.Payload)
		}
	}
}

// ingest takes one event: a human's message, from someone Spool allows,
// in the loop's owner DM or its channel, with the files it shares, and a
// person's edit or deletion of one the hub holds. Everything else is
// dropped here: joins and other subtypes, and every bot's post, the loops'
// own mirrored ones included, which would otherwise come back in as new.
func (adapter *Adapter) ingest(ctx context.Context, link *link, payload json.RawMessage) {
	var callback eventCallback
	if err := json.Unmarshal(payload, &callback); err != nil {
		adapter.log.Warn("slack: unreadable event", "loop", link.loopID, "err", err)
		return
	}
	event := callback.Event
	if event.Type == "reaction_added" || event.Type == "reaction_removed" {
		adapter.ingestReaction(ctx, link, event)
		return
	}
	if event.Type == "message" && (event.Subtype == "message_changed" || event.Subtype == "message_deleted") {
		adapter.ingestEdit(ctx, link, event)
		return
	}
	if event.Type != "message" || (event.Subtype != "" && event.Subtype != "file_share") ||
		event.BotID != "" || event.User == "" {
		return
	}
	// a deleted file is not shared, and a message of nothing else says
	// nothing
	event.Files = slices.DeleteFunc(event.Files, func(file File) bool { return file.Mode == "tombstone" })
	if strings.TrimSpace(event.Text) == "" && len(event.Files) == 0 {
		return
	}
	loopRecord, err := adapter.store.Loops().Get(ctx, link.loopID)
	if err != nil {
		adapter.log.Error("slack: read loop for an event", "loop", link.loopID, "err", err)
		return
	}
	if event.User == loopRecord.SlackBotUserID {
		return
	}
	if event.Subtype == "file_share" && adapter.isLoopApp(ctx, event.User) {
		// a loop's file, which its app shares as its bot user and with no
		// bot_id to tell it by
		return
	}
	switch event.ChannelType {
	case "im":
		adapter.ingestDM(ctx, link, loopRecord, callback.TeamID, event)
	case "channel", "group":
		adapter.ingestChannel(ctx, link, loopRecord, callback.TeamID, event)
	}
}

// ingestReaction hands the router a person's reaction added to or removed
// from a hub message (ADR-0040). Every loop's app in the channel hears it,
// and each reports it: the hub keeps one row per reactor, emoji and
// message, so no app is elected. A reaction passes the sender gate a
// message does, and a stranger's is dropped, as their message is
// (ADR-0051). A loop's own reaction, which its app made, is already
// the hub's.
func (adapter *Adapter) ingestReaction(ctx context.Context, link *link, event messageEvent) {
	if event.Item.Type != "message" || event.User == "" || event.Reaction == "" {
		return
	}
	sender := adapter.eventSender(ctx, link, event.User)
	if sender == nil {
		return
	}
	target, err := adapter.store.Messages().BySlackTS(ctx, event.Item.Channel, event.Item.TS)
	if err != nil {
		return // a message the hub never held
	}
	err = adapter.router.React(ctx, route.InboundReaction{MessageID: target.ID, Reactor: authorName(sender),
		ReactorKey: store.PersonReactor(store.SurfaceSlack, event.User), Emoji: emojiOf(event.Reaction),
		Removed: event.Type == "reaction_removed"})
	if err != nil {
		adapter.log.Error("slack: record reaction", "loop", link.loopID, "message", target.ID, "err", err)
	}
}

// ingestEdit hands the router a person's edit or deletion of a message the
// hub holds, found by its channel and ts as a reaction's target is
// (ADR-0055). Every loop's app in the channel hears it, and each reports
// it: the key is the hub's id for the original and Slack's ts for the
// edit, or the deletion, the same whichever app reports it, so the
// correction is stored once. Only an edit of a message's words counts, not
// a link unfurling under it. An edit passes the sender gate a reaction
// does. One of a message the hub never held, or of a bot's, is dropped.
func (adapter *Adapter) ingestEdit(ctx context.Context, link *link, event messageEvent) {
	var user, ts, text, kind, key string
	switch {
	case event.Subtype == "message_changed" && event.Message != nil && event.Message.Edited != nil:
		changed := event.Message
		if changed.BotID != "" || (event.PreviousMessage != nil && event.PreviousMessage.Text == changed.Text) {
			return
		}
		user, ts, text, kind, key = changed.User, changed.TS, changed.Text, store.EditEdited, changed.Edited.TS
	case event.Subtype == "message_deleted" && event.PreviousMessage != nil:
		if event.PreviousMessage.BotID != "" {
			return
		}
		user, ts, kind, key = event.PreviousMessage.User, event.DeletedTS, store.EditDeleted, store.EditDeleted
	default:
		return
	}
	if user == "" || ts == "" {
		return
	}
	sender := adapter.eventSender(ctx, link, user)
	if sender == nil {
		return
	}
	original, err := adapter.store.Messages().BySlackTS(ctx, event.Channel, ts)
	if err != nil || original.FromLoopID != "" {
		return
	}
	err = adapter.router.Correct(ctx, route.InboundEdit{Original: original, Author: authorName(sender),
		Text: adapter.readable(ctx, text), Kind: kind, Key: fmt.Sprintf("slack:%d:%s", original.ID, key)})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		adapter.log.Error("slack: record an edit", "loop", link.loopID, "message", original.ID, "err", err)
	}
}

// ingestChannel takes a channel message into the channel its room carries
// (ADR-0038). A Slack channel the app has no room for is recorded as one,
// unbound, for the operator to bind from the loop page; a message there is
// counted and goes no further, which is the control room's hint that the
// app was invited somewhere it carries nothing yet.
//
// Every loop's app in the channel hears the message, and Slack gives each
// the same ts for it. So the ingest election is the store's key on the
// channel and ts (ADR-0020): the first link to insert the message stores
// and delivers it, and the others find it there. A room carries one
// channel for every loop that binds it, so whichever link wins, the
// message lands in the same channel.
func (adapter *Adapter) ingestChannel(ctx context.Context, link *link, loopRecord *store.Loop, teamID string, event messageEvent) {
	sender := adapter.allowedSender(ctx, loopRecord, teamID, event)
	if sender == nil {
		return
	}
	channel := adapter.roomChannel(ctx, link, loopRecord, event.Channel)
	if channel == "" {
		link.ignore()
		return
	}
	err := adapter.router.Ingest(ctx, route.InboundMessage{
		Origin:         store.OriginSlackChannel,
		Channel:        channel,
		Author:         authorName(sender),
		Text:           adapter.readable(ctx, event.Text),
		SlackChannelID: event.Channel,
		SlackTS:        event.TS,
		ReplyToID:      adapter.threadRoot(ctx, event),
		Attachments:    adapter.attachmentsOf(ctx, link, loopRecord, event),
	})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		adapter.log.Error("slack channel ingest", "loop", loopRecord.Name, "err", err)
	}
}

// roomChannel is the hub channel a Slack channel carries for this loop's
// app, "" for none. A Slack channel the app has no room for is recorded as
// one, unbound, under the name Slack gives it; any room still untitled is
// named the same way. The one bound without
// asking is the first a loop hears an allowed sender in while its fleet
// channel has no room, as a Telegram bot's first group is: a new fleet
// still binds by its first message, and a later channel never moves it.
func (adapter *Adapter) roomChannel(ctx context.Context, link *link, loopRecord *store.Loop, channelID string) string {
	now := time.Now().UnixMilli()
	room, added, err := adapter.store.Rooms().Sight(ctx, &store.Room{LoopID: loopRecord.ID, Surface: store.SurfaceSlack,
		RoomID: channelID, FirstSeenAt: now})
	if err != nil {
		adapter.log.Error("slack: record room", "loop", loopRecord.Name, "err", err)
		return ""
	}
	if room.Title == "" {
		// a room bound before the app heard from it, by a pasted id or as
		// the fleet room an upgrade made, is named at its first message
		adapter.titleRoom(ctx, loopRecord, channelID, now)
	}
	if room.Channel != "" {
		return room.Channel
	}
	if loopRecord.SlackChannelID == "" {
		_, err := adapter.store.Rooms().Bind(ctx, loopRecord.ID, store.SurfaceSlack, channelID, store.FleetChannel, now)
		switch {
		case err == nil:
			adapter.log.Info("slack channel bound", "loop", loopRecord.Name, "channel", channelID)
			adapter.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
				"loop_id": loopRecord.ID, "name": loopRecord.Name, "slack_channel_bound": true, "rooms_changed": true,
			}})
			adapter.nameChannel(ctx, link, loopRecord, channelID)
			return store.FleetChannel
		case !errors.Is(err, store.ErrRoomInUse):
			adapter.log.Error("slack: bind channel", "loop", loopRecord.Name, "err", err)
			return ""
		}
		// another loop's room for another channel: this one waits unbound
	}
	if added {
		adapter.log.Info("slack room recorded unbound", "loop", loopRecord.Name, "channel", channelID)
		adapter.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
			"loop_id": loopRecord.ID, "name": loopRecord.Name, "rooms_changed": true,
		}})
	}
	return ""
}

// titleRoom names a room the way the loop page lists it, with the name
// Slack gives the channel. A channel Slack will not name is listed by its
// id, and asked about again at its next message.
func (adapter *Adapter) titleRoom(ctx context.Context, loopRecord *store.Loop, channelID string, now int64) {
	name, err := adapter.client.ChannelName(ctx, loopRecord.SlackBotToken, channelID)
	if err != nil {
		adapter.log.Warn("slack: name a room", "loop", loopRecord.Name, "channel", channelID, "err", err)
		return
	}
	if _, _, err := adapter.store.Rooms().Sight(ctx, &store.Room{LoopID: loopRecord.ID, Surface: store.SurfaceSlack,
		RoomID: channelID, Title: name, FirstSeenAt: now}); err != nil {
		adapter.log.Error("slack: title room", "loop", loopRecord.Name, "err", err)
	}
}

// ingestDM takes a message in the app's DM. The owner's is the loop's
// owner_dm; any other allowed sender's is a conversation of their own,
// dm:<name>, which the loop answers there and nowhere else (ADR-0049).
func (adapter *Adapter) ingestDM(ctx context.Context, link *link, loopRecord *store.Loop, teamID string, event messageEvent) {
	sender := adapter.allowedSender(ctx, loopRecord, teamID, event)
	if sender == nil {
		return
	}
	// The message records who it is with, owner or not, which is who
	// reads it (ADR-0050).
	var conversation string
	person := store.SlackPerson(event.User)
	switch {
	case loopRecord.OwnerSlackUserID == "" || loopRecord.OwnerSlackUserID != event.User:
		conversation = store.DMDestinationPrefix + store.DMName(sender.Username, event.User)
		adapter.capturePrivateChat(ctx, loopRecord, person, event.Channel)
	case loopRecord.OwnerSlackDMChannel != event.Channel:
		adapter.captureOwnerDM(ctx, loopRecord, event.Channel)
	}
	// A DM that is a link code, or the YES confirming one, links its sender
	// to a control room user, and goes no further (ADR-0051).
	if text, ok := adapter.router.LinkCode(ctx, adapter.linkAttempt(loopRecord, teamID, event, true)); ok {
		if text != "" {
			adapter.notify(loopRecord.ID, notice{channel: event.Channel, text: escape(text)})
		}
		return
	}
	err := adapter.router.Ingest(ctx, route.InboundMessage{
		Origin:         store.OriginSlackDM,
		Author:         authorName(sender),
		Text:           adapter.readable(ctx, event.Text),
		SlackChannelID: event.Channel,
		SlackTS:        event.TS,
		ReplyToID:      adapter.threadRoot(ctx, event),
		ImplicitTo:     loopRecord.ID,
		Attachments:    adapter.attachmentsOf(ctx, link, loopRecord, event),
		Conversation:   conversation,
		DMPerson:       person,
	})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		adapter.log.Error("slack dm ingest", "loop", loopRecord.Name, "err", err)
	}
}

// allowedSender is the event's sender if Spool allows them, and nil
// otherwise. Workspace membership admits nobody: a sender Spool does not
// allow is a stranger, whose DM may be a link code, or the YES that
// confirms one, and is otherwise answered with nothing: no pending
// record, no reply, and the text kept nowhere (ADR-0051).
func (adapter *Adapter) allowedSender(ctx context.Context, loopRecord *store.Loop, teamID string, event messageEvent) *store.SlackSender {
	sender, err := adapter.store.SlackSenders().Get(ctx, event.User)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		adapter.log.Error("slack: sender lookup", "loop", loopRecord.Name, "err", err)
		adapter.turnedAway(loopRecord, event, "sender lookup failed")
		return nil
	case sender.Status == store.SenderAllowed:
		return sender
	case sender.Status == store.SenderBlocked:
		adapter.router.Blocked(store.SlackPerson(event.User))
		return nil
	}
	if event.ChannelType == "im" {
		if text, ok := adapter.router.LinkCode(ctx, adapter.linkAttempt(loopRecord, teamID, event, false)); ok {
			if text != "" {
				adapter.notify(loopRecord.ID, notice{channel: event.Channel, text: escape(text)})
			}
			return nil
		}
	}
	adapter.router.Stranger(store.SlackPerson(event.User), event.ChannelType == "im")
	return nil
}

// eventSender is the sender gate for a reaction or a click on a poll: the
// sender if Spool allows them, and nil otherwise. Anyone else's is dropped
// and counted, as their message is, and logged once an hour a sender
// (#788).
func (adapter *Adapter) eventSender(ctx context.Context, link *link, userID string) *store.SlackSender {
	sender, err := adapter.store.SlackSenders().Get(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		adapter.log.Error("slack: sender lookup", "loop", link.loopID, "err", err)
		return nil // fail closed
	case sender.Status == store.SenderAllowed:
		return sender
	case sender.Status == store.SenderBlocked:
		adapter.router.Blocked(store.SlackPerson(userID))
		return nil
	}
	adapter.router.Stranger(store.SlackPerson(userID), false)
	return nil
}

// linkAttempt is a DM to the loop's app as the router's link check takes
// it. Who the sender is is asked of Slack only for a code the check takes
// up, so a stranger's every DM costs no call; a sender Slack will not
// describe is linked by id alone.
func (adapter *Adapter) linkAttempt(loopRecord *store.Loop, teamID string, event messageEvent, allowed bool) route.LinkAttempt {
	if teamID == "" {
		teamID = loopRecord.SlackTeamID
	}
	return route.LinkAttempt{Text: event.Text, Person: store.SlackPerson(event.User), LoopID: loopRecord.ID,
		TeamID: teamID, Allowed: allowed,
		Describe: func(ctx context.Context) (string, string) {
			who, err := adapter.client.UserInfo(ctx, loopRecord.SlackBotToken, event.User)
			if err != nil {
				adapter.log.Warn("slack: describe a sender", "loop", loopRecord.Name, "user", event.User, "err", err)
				return "", ""
			}
			return who.Name, who.DisplayName
		}}
}

// turnedAway records a message that reached a loop's app and goes no
// further: a person whose words went nowhere, who has no way to tell, so
// the reason is logged (#161). A pending sender in a DM, and a sender DMing
// a loop that is not theirs, are also told (notice.go).
func (adapter *Adapter) turnedAway(loopRecord *store.Loop, event messageEvent, reason string) {
	adapter.log.Info("slack inbound discarded", "loop", loopRecord.Name, "reason", reason,
		"user", event.User, "channel_type", event.ChannelType, "channel", event.Channel, "ts", event.TS)
}

// nameChannel asks Slack what the loop's bound channel is called, for the
// control room to show. A channel it will not name keeps showing its id.
func (adapter *Adapter) nameChannel(ctx context.Context, link *link, loopRecord *store.Loop, channelID string) {
	if channelID == "" {
		return
	}
	name, err := adapter.client.ChannelName(ctx, loopRecord.SlackBotToken, channelID)
	if err != nil {
		adapter.log.Warn("slack: name the bound channel", "loop", loopRecord.Name, "channel", channelID, "err", err)
		return
	}
	link.named(name)
}

// captureOwnerDM records the DM the owner wrote to the app in, which is
// the one the app answers them in.
func (adapter *Adapter) captureOwnerDM(ctx context.Context, loopRecord *store.Loop, channelID string) {
	if err := adapter.store.Loops().SetSlackOwnerDM(ctx, loopRecord.ID, channelID, time.Now().UnixMilli()); err != nil {
		adapter.log.Error("slack: owner dm capture", "loop", loopRecord.Name, "err", err)
		return
	}
	adapter.log.Info("slack owner dm captured", "loop", loopRecord.Name, "channel", channelID)
	adapter.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
		"loop_id": loopRecord.ID, "name": loopRecord.Name, "owner_dm_ready": true,
	}})
}

// capturePrivateChat records the DM a person other than the owner wrote to
// the app in, which is the one the app answers them in, and what lets the
// loop write to them first later (ADR-0049).
func (adapter *Adapter) capturePrivateChat(ctx context.Context, loopRecord *store.Loop, person, channelID string) {
	err := adapter.store.PrivateChats().Capture(ctx, &store.PrivateChat{
		LoopID: loopRecord.ID, Person: person, Chat: channelID, CapturedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		adapter.log.Error("slack: private chat capture", "loop", loopRecord.Name, "err", err)
	}
}

// threadRoot is the message a thread reply answers: the thread's first
// message, which is what Slack names on every reply in it. 0 for a message
// that is not in a thread, starts one, or answers a message this hub never
// stored.
func (adapter *Adapter) threadRoot(ctx context.Context, event messageEvent) int64 {
	if event.ThreadTS == "" || event.ThreadTS == event.TS {
		return 0
	}
	root, err := adapter.store.Messages().BySlackTS(ctx, event.Channel, event.ThreadTS)
	if err != nil {
		return 0
	}
	return root.ID
}

// isLoopApp reports whether userID is any loop's app.
func (adapter *Adapter) isLoopApp(ctx context.Context, userID string) bool {
	loops, err := adapter.store.Loops().List(ctx)
	if err != nil {
		adapter.log.Error("slack: list loops for a file share", "err", err)
		return false
	}
	for _, loopRecord := range loops {
		if loopRecord.SlackBotUserID == userID {
			return true
		}
	}
	return false
}

// attachmentsOf is the files a message shares, as the router takes them:
// downloaded as the app that heard the message, and only if the message is
// stored (#123). A deleted file is left out; one Slack no longer shows is
// recorded as not kept. A file the event gives by its id alone, as Slack
// does for one shared from another organization, is asked about first, so
// it is named as its sender named it.
func (adapter *Adapter) attachmentsOf(ctx context.Context, link *link, loopRecord *store.Loop, event messageEvent) []route.InboundAttachment {
	var out []route.InboundAttachment
	for _, file := range event.Files {
		var undescribed error
		if file.FileAccess == "check_file_info" {
			if described, err := adapter.client.FileInfo(ctx, loopRecord.SlackBotToken, file.ID); err == nil {
				file = *described
			} else {
				undescribed = err
				adapter.log.Warn("slack: describe a shared file", "loop", loopRecord.Name, "file", file.ID, "err", err)
			}
		}
		if file.Mode == "tombstone" {
			continue
		}
		item := route.InboundAttachment{Name: file.Name, Size: file.Size}
		if item.Name == "" {
			item.Name = "file-" + file.ID
		}
		if strings.HasPrefix(file.Mimetype, "image/") {
			item.Kind = store.AttachmentImage
		}
		var apiErr *APIError
		switch {
		case errors.As(undescribed, &apiErr) && apiErr.Code == "missing_scope":
			// files.info needs files:read too, and says so outright.
			refusal := fmt.Errorf("%w: %w", undescribed, route.ErrNoFileAccess)
			item.Fetch = link.watchFetch(func(context.Context) (io.ReadCloser, error) { return nil, refusal })
		case file.Mode != "hidden_by_limit" && file.DownloadURL != "":
			botToken, fileURL := loopRecord.SlackBotToken, file.DownloadURL
			item.Fetch = link.watchFetch(func(ctx context.Context) (io.ReadCloser, error) {
				return adapter.client.Download(ctx, botToken, fileURL)
			})
		}
		out = append(out, item)
	}
	return out
}

// authorName is how a sender is named to a loop: their handle, which a
// loop can mention them by, or failing that whatever Slack gave.
func authorName(sender *store.SlackSender) string {
	switch {
	case sender.Username != "":
		return sender.Username
	case sender.Display != "":
		return sender.Display
	}
	return sender.SlackUserID
}

// markup is Slack's inline syntax: <@U…> a user, <#C…|name> a channel,
// <!here> a broadcast, and <url|label> a link, each with an optional label
// after a bar.
var markup = regexp.MustCompile(`<([^<>|]*)(?:\|([^<>]*))?>`)

// readable turns a message's Slack markup into the text a loop reads. A
// mention of a loop's app becomes @ and the loop's name, which is how the
// router finds whom a message addresses; a mention of a sender Spool knows
// becomes their handle. Slack escapes &, < and > in text, and the escapes
// are undone last, so a literal "&lt;" someone typed is not read as markup.
func (adapter *Adapter) readable(ctx context.Context, text string) string {
	loops, err := adapter.store.Loops().List(ctx)
	if err != nil {
		adapter.log.Error("slack: resolve mentions", "err", err)
	}
	loopOf := map[string]string{} // bot user id → loop name
	for _, loopRecord := range loops {
		if loopRecord.SlackBotUserID != "" {
			loopOf[loopRecord.SlackBotUserID] = loopRecord.Name
		}
	}
	text = markup.ReplaceAllStringFunc(text, func(token string) string {
		parts := markup.FindStringSubmatch(token)
		target, label := parts[1], parts[2]
		switch {
		case strings.HasPrefix(target, "@"):
			userID := strings.TrimPrefix(target, "@")
			if name, ok := loopOf[userID]; ok {
				return "@" + name
			}
			if sender, err := adapter.store.SlackSenders().Get(ctx, userID); err == nil {
				return "@" + authorName(sender)
			}
			if label != "" {
				return "@" + strings.TrimPrefix(label, "@")
			}
			return "@" + userID
		case strings.HasPrefix(target, "#"):
			if label != "" {
				return "#" + label
			}
			return target
		case strings.HasPrefix(target, "!"):
			// <!here>, <!channel>, <!subteam^S…|@team>: named as Slack
			// shows them
			if label != "" {
				return label
			}
			return "@" + strings.TrimPrefix(target, "!")
		case label != "" && label != target:
			return label + " (" + target + ")"
		}
		return target
	})
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
}
