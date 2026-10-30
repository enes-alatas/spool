package telegram

import (
	"slices"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/route"
)

// A person who sends several photos or files together sends an album, and
// means one message. Telegram delivers each item as a message of its own,
// sharing a media_group_id, and any item may carry words of its own. The
// poller holds the items until the album stops growing and takes it as one
// message with every file attached, as a Slack message carries several
// files (#1199).

const (
	// albumSettle is how long an album waits after its latest item before
	// it is taken as complete. Telegram sends the items of one album within
	// a second or so of each other.
	albumSettle = 1500 * time.Millisecond
	// maxAlbumItems is the most items Telegram puts in one album; an album
	// that reaches it is complete without waiting.
	maxAlbumItems = 10
)

// albumKey names an album: its id is Telegram's, unique within its chat,
// and the sender, so items from two senders never join one message. Only
// the first item's sender is checked against the allowlist, so an item from
// anyone else must make an album of its own, which is checked on its own.
type albumKey struct {
	chatID       int64
	senderID     int64
	senderChatID int64
	groupID      string
}

func albumKeyOf(message *tgMsgAlias) albumKey {
	key := albumKey{chatID: message.Chat.ID, groupID: message.MediaGroupID}
	if message.From != nil {
		key.senderID = message.From.ID
	}
	if message.SenderChat != nil {
		key.senderChatID = message.SenderChat.ID
	}
	return key
}

// album is the items of one album received so far, the update that brought
// the first of them, and when the latest arrived.
type album struct {
	items       []*tgMsgAlias
	firstUpdate int64
	latest      time.Time
}

// albums holds the albums one poller is still receiving, in the order they
// began. Only that poller's goroutine touches it.
type albums struct {
	pending map[albumKey]*album
	order   []albumKey
}

func newAlbums() *albums {
	return &albums{pending: map[albumKey]*album{}}
}

// waiting reports whether any album is still being received.
func (held *albums) waiting() bool { return len(held.order) > 0 }

// earliest is the first update of any album still held: the offset the
// poller confirms no further than, so a restart replays every held item.
func (held *albums) earliest() (int64, bool) {
	first, ok := int64(0), false
	for _, growing := range held.pending {
		if !ok || growing.firstUpdate < first {
			first, ok = growing.firstUpdate, true
		}
	}
	return first, ok
}

// nextSettle is when the next held album settles, unless an item joins it.
func (held *albums) nextSettle() time.Time {
	var next time.Time
	for _, growing := range held.pending {
		if at := growing.latest.Add(albumSettle); next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}

// add holds one item of an album, brought by update updateID, and returns
// the album when that item completes it.
func (held *albums) add(message *tgMsgAlias, updateID int64, now time.Time) *album {
	key := albumKeyOf(message)
	growing, ok := held.pending[key]
	if !ok {
		growing = &album{firstUpdate: updateID}
		held.pending[key] = growing
		held.order = append(held.order, key)
	}
	growing.items = append(growing.items, message)
	growing.latest = now
	if len(growing.items) < maxAlbumItems {
		return nil
	}
	held.remove(key)
	return growing
}

// settled removes and returns, in the order they began, the albums that
// have had no new item for albumSettle.
func (held *albums) settled(now time.Time) []*album {
	return held.take(func(key albumKey) bool {
		return now.Sub(held.pending[key].latest) >= albumSettle
	})
}

// inChat removes and returns, in the order they began, the albums still
// being received in a chat. A message that follows them there was sent
// after them, so they reach the loop first.
func (held *albums) inChat(chatID int64) []*album {
	return held.take(func(key albumKey) bool { return key.chatID == chatID })
}

func (held *albums) take(done func(albumKey) bool) []*album {
	var out []*album
	for _, key := range slices.Clone(held.order) {
		if done(key) {
			out = append(out, held.pending[key])
			held.remove(key)
		}
	}
	return out
}

func (held *albums) remove(key albumKey) {
	delete(held.pending, key)
	held.order = slices.DeleteFunc(held.order, func(other albumKey) bool { return other == key })
}

// asOneMessage is the album as the message the person meant: the first
// item's ids, date and sender, every item's caption, one per line, and
// every item's file, in the order they were sent.
func (whole *album) asOneMessage(bot *poller) (*tgMsgAlias, []route.InboundAttachment) {
	items := slices.Clone(whole.items)
	slices.SortFunc(items, func(left, right *tgMsgAlias) int {
		return int(left.MessageID - right.MessageID)
	})
	first := *items[0]
	var captions []string
	var attachments []route.InboundAttachment
	for _, item := range items {
		if caption := strings.TrimSpace(item.Caption); caption != "" {
			captions = append(captions, caption)
		}
		if first.ReplyToMessage == nil {
			first.ReplyToMessage = item.ReplyToMessage
		}
		attachments = append(attachments, attachmentsOf(bot, item)...)
	}
	first.Caption = strings.Join(captions, "\n")
	return &first, attachments
}
