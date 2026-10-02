package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// Inbound: what a loop's app hears over Socket Mode becomes a hub message,
// or a reaction on one. The app subscribes to message.im, message.channels
// and message.groups, and to reaction_added and reaction_removed (the
// manifest the control room hands out), so every event that matters is a
// message, in the owner's DM or in a channel, or a reaction to one.

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
}

// events is how many envelopes a link holds for ingest while it keeps
// reading. Ingest can call users.info, and a read loop that waited on it
// would leave the ping's answer unread.
const events = 256

// ingestLoop hands a link's events to ingest one at a time, in the order
// Slack sent them, until the link closes the channel. What is left once
// the link is stopping is drained, not ingested.
func (adapter *Adapter) ingestLoop(ctx context.Context, link *link, payloads <-chan json.RawMessage) {
	for payload := range payloads {
		if ctx.Err() == nil {
			adapter.ingest(ctx, link, payload)
		}
	}
}

// ingest takes one event: a human's message, from someone Spool allows,
// in the loop's owner DM or its channel, with the files it shares. Everything
// else is dropped here: edits, joins and other subtypes, and every bot's
// post, the loops' own mirrored ones included, which would otherwise come
// back in as new.
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
		adapter.ingestDM(ctx, loopRecord, callback.TeamID, event)
	case "channel", "group":
		adapter.ingestChannel(ctx, link, loopRecord, callback.TeamID, event)
	}
}

// ingestReaction hands the router a person's reaction added to or removed
// from a hub message (ADR-0040). Every loop's app in the channel hears it,
// and each reports it: the hub keeps one row per reactor, emoji and
// message, so no app is elected. A reaction passes the sender gate a
// message does, but registers nobody: a stranger is given a pairing code
// when they write. A loop's own reaction, which its app made, is already
// the hub's.
func (adapter *Adapter) ingestReaction(ctx context.Context, link *link, event messageEvent) {
	if event.Item.Type != "message" || event.User == "" || event.Reaction == "" {
		return
	}
	sender, err := adapter.store.SlackSenders().Get(ctx, event.User)
	if err != nil || sender.Status != store.SenderAllowed {
		adapter.log.Info("slack reaction discarded", "loop", link.loopID, "reason", "sender is not allowed",
			"user", event.User, "channel", event.Item.Channel, "ts", event.Item.TS)
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

// ingestChannel takes a channel message into the fleet channel. A loop's
// app hears one channel, the first it hears an allowed sender in, as a
// Telegram bot binds its group; a message from any other is counted, which
// is the control room's hint that the app was invited somewhere else.
//
// Every loop's app in the channel hears the message, and Slack gives each
// the same ts for it. So the ingest election is the store's key on the
// channel and ts (ADR-0020): the first link to insert the message stores
// and delivers it, and the others find it there.
func (adapter *Adapter) ingestChannel(ctx context.Context, link *link, loopRecord *store.Loop, teamID string, event messageEvent) {
	if loopRecord.SlackChannelID != "" && loopRecord.SlackChannelID != event.Channel {
		link.ignore()
		return
	}
	sender := adapter.allowedSender(ctx, loopRecord, teamID, event, "group:"+loopRecord.Name)
	if sender == nil {
		return
	}
	if loopRecord.SlackChannelID == "" {
		adapter.bindChannel(ctx, loopRecord, event.Channel)
		adapter.nameChannel(ctx, link, loopRecord, event.Channel)
	}
	err := adapter.router.Ingest(ctx, route.InboundMessage{
		Origin:         store.OriginSlackChannel,
		Author:         authorName(sender),
		Text:           adapter.readable(ctx, event.Text),
		SlackChannelID: event.Channel,
		SlackTS:        event.TS,
		ReplyToID:      adapter.threadRoot(ctx, event),
		Attachments:    adapter.attachmentsOf(ctx, loopRecord, event),
	})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		adapter.log.Error("slack channel ingest", "loop", loopRecord.Name, "err", err)
	}
}

// ingestDM takes a message in the app's DM, which reaches the loop only
// from its owner: owner_dm addresses the owner, so a loop that answered
// anyone else would answer into someone else's DM.
func (adapter *Adapter) ingestDM(ctx context.Context, loopRecord *store.Loop, teamID string, event messageEvent) {
	sender := adapter.allowedSender(ctx, loopRecord, teamID, event, "dm:"+loopRecord.Name)
	if sender == nil {
		return
	}
	if loopRecord.OwnerSlackUserID == "" || loopRecord.OwnerSlackUserID != event.User {
		adapter.turnedAway(loopRecord, event, "sender is not this loop's owner")
		adapter.tellNotOwner(loopRecord, event)
		return
	}
	if loopRecord.OwnerSlackDMChannel != event.Channel {
		adapter.captureOwnerDM(ctx, loopRecord, event.Channel)
	}
	err := adapter.router.Ingest(ctx, route.InboundMessage{
		Origin:         store.OriginSlackDM,
		Author:         authorName(sender),
		Text:           adapter.readable(ctx, event.Text),
		SlackChannelID: event.Channel,
		SlackTS:        event.TS,
		ReplyToID:      adapter.threadRoot(ctx, event),
		ImplicitTo:     loopRecord.ID,
		Attachments:    adapter.attachmentsOf(ctx, loopRecord, event),
	})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		adapter.log.Error("slack dm ingest", "loop", loopRecord.Name, "err", err)
	}
}

// allowedSender is the event's sender if Spool allows them, and nil
// otherwise. Workspace membership admits nobody: a sender Spool has not
// seen is registered as pending, with a pairing code, for the operator to
// allow in the control room.
func (adapter *Adapter) allowedSender(ctx context.Context, loopRecord *store.Loop, teamID string, event messageEvent, via string) *store.SlackSender {
	senders := adapter.store.SlackSenders()
	sender, err := senders.Get(ctx, event.User)
	if errors.Is(err, store.ErrNotFound) {
		sender, err = adapter.registerSender(ctx, loopRecord, teamID, event.User, via)
	}
	if err != nil {
		adapter.log.Error("slack: sender lookup", "loop", loopRecord.Name, "err", err)
		adapter.turnedAway(loopRecord, event, "sender lookup failed")
		return nil
	}
	switch sender.Status {
	case store.SenderAllowed:
		return sender
	case store.SenderBlocked:
		adapter.turnedAway(loopRecord, event, "sender is blocked")
	default:
		adapter.turnedAway(loopRecord, event, "sender is pending approval")
		adapter.tellPairCode(loopRecord, sender, event)
	}
	return nil
}

// registerSender records a sender Spool has not seen as pending. Who they
// are is asked of Slack, so the control room can show a name; a sender
// Slack will not describe is registered by id alone rather than lost.
func (adapter *Adapter) registerSender(ctx context.Context, loopRecord *store.Loop, teamID, userID, via string) (*store.SlackSender, error) {
	if teamID == "" {
		teamID = loopRecord.SlackTeamID
	}
	now := time.Now().UnixMilli()
	sender := &store.SlackSender{SlackUserID: userID, TeamID: teamID, Status: store.SenderPending,
		PairCode: surface.PairCode(), FirstSeenVia: via, CreatedAt: now, UpdatedAt: now}
	if who, err := adapter.client.UserInfo(ctx, loopRecord.SlackBotToken, userID); err == nil {
		sender.Username, sender.Display = who.Name, who.DisplayName
	} else {
		adapter.log.Warn("slack: describe a new sender", "loop", loopRecord.Name, "user", userID, "err", err)
	}
	err := adapter.store.SlackSenders().Create(ctx, sender)
	if errors.Is(err, store.ErrDuplicate) {
		// another loop's link registered them first
		return adapter.store.SlackSenders().Get(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	adapter.log.Info("new slack sender pending approval", "user", authorName(sender), "id", userID)
	adapter.bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender.Frame()})
	return sender, nil
}

// turnedAway records a message that reached a loop's app and goes no
// further: a person whose words went nowhere, who has no way to tell, so
// the reason is logged (#161). A pending sender in a DM, and a sender DMing
// a loop that is not theirs, are also told (notice.go).
func (adapter *Adapter) turnedAway(loopRecord *store.Loop, event messageEvent, reason string) {
	adapter.log.Info("slack inbound discarded", "loop", loopRecord.Name, "reason", reason,
		"user", event.User, "channel_type", event.ChannelType, "channel", event.Channel, "ts", event.TS)
}

func (adapter *Adapter) bindChannel(ctx context.Context, loopRecord *store.Loop, channelID string) {
	boundAt := time.Now().UnixMilli()
	if err := adapter.store.Loops().SetSlackBinding(ctx, loopRecord.ID, channelID, boundAt, boundAt); err != nil {
		adapter.log.Error("slack: bind channel", "loop", loopRecord.Name, "err", err)
		return
	}
	adapter.log.Info("slack channel bound", "loop", loopRecord.Name, "channel", channelID)
	adapter.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
		"loop_id": loopRecord.ID, "name": loopRecord.Name, "slack_channel_bound": true,
	}})
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
func (adapter *Adapter) attachmentsOf(ctx context.Context, loopRecord *store.Loop, event messageEvent) []route.InboundAttachment {
	var out []route.InboundAttachment
	for _, file := range event.Files {
		if file.FileAccess == "check_file_info" {
			if described, err := adapter.client.FileInfo(ctx, loopRecord.SlackBotToken, file.ID); err == nil {
				file = *described
			} else {
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
		if file.Mode != "hidden_by_limit" && file.DownloadURL != "" {
			botToken, fileURL := loopRecord.SlackBotToken, file.DownloadURL
			item.Fetch = func(ctx context.Context) (io.ReadCloser, error) {
				return adapter.client.Download(ctx, botToken, fileURL)
			}
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
