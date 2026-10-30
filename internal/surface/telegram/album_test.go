package telegram

import (
	"slices"
	"testing"
	"time"
)

func albumItem(chatID, messageID int64, groupID, caption string) *tgMsgAlias {
	return &tgMsgAlias{
		MessageID:    messageID,
		Chat:         Chat{ID: chatID},
		Caption:      caption,
		MediaGroupID: groupID,
		Photo:        []PhotoSize{{FileID: "file", FileSize: 1}},
	}
}

func TestAnAlbumIsHeldUntilItSettles(t *testing.T) {
	held := newAlbums()
	start := time.Unix(1_000, 0)
	held.add(albumItem(1, 10, "g", "two shots"), 0, start)
	held.add(albumItem(1, 11, "g", ""), 0, start.Add(time.Second))
	if want := start.Add(time.Second + albumSettle); !held.nextSettle().Equal(want) {
		t.Errorf("nextSettle = %s, want albumSettle after the latest item, %s", held.nextSettle(), want)
	}
	if got := held.settled(start.Add(time.Second + albumSettle - time.Millisecond)); len(got) != 0 {
		t.Fatalf("an album settled %s after its latest item, before albumSettle", albumSettle-time.Millisecond)
	}
	got := held.settled(start.Add(time.Second + albumSettle))
	if len(got) != 1 || len(got[0].items) != 2 {
		t.Fatalf("settled = %v, want the one album with both items", got)
	}
	if held.waiting() {
		t.Error("a settled album is still held")
	}
}

func TestAFullAlbumNeedNotWait(t *testing.T) {
	held := newAlbums()
	now := time.Unix(1_000, 0)
	for id := int64(1); id < maxAlbumItems; id++ {
		if whole := held.add(albumItem(1, id, "g", ""), 0, now); whole != nil {
			t.Fatalf("the album was complete at %d items", id)
		}
	}
	whole := held.add(albumItem(1, maxAlbumItems, "g", ""), 0, now)
	if whole == nil || len(whole.items) != maxAlbumItems {
		t.Fatalf("the %dth item did not complete the album", maxAlbumItems)
	}
	if held.waiting() {
		t.Error("a completed album is still held")
	}
}

// A message that follows an album in its chat releases the album first, so
// the two reach the loop in the order they were sent; another chat's album
// keeps waiting.
func TestAMessageReleasesTheAlbumsAheadOfItInItsChat(t *testing.T) {
	held := newAlbums()
	now := time.Unix(1_000, 0)
	held.add(albumItem(1, 10, "first", ""), 0, now)
	held.add(albumItem(2, 20, "elsewhere", ""), 0, now)
	held.add(albumItem(1, 12, "second", ""), 0, now)
	got := held.inChat(1)
	if len(got) != 2 || got[0].items[0].MediaGroupID != "first" || got[1].items[0].MediaGroupID != "second" {
		t.Fatalf("inChat(1) = %v, want albums first and second, in that order", got)
	}
	if !held.waiting() || len(held.inChat(2)) != 1 {
		t.Error("another chat's album was released too")
	}
}

// The poller confirms no update past the first item of any album it holds,
// so a restart replays them all; with nothing held it confirms freely.
func TestTheEarliestHeldUpdateBoundsTheOffset(t *testing.T) {
	held := newAlbums()
	now := time.Unix(1_000, 0)
	if _, ok := held.earliest(); ok {
		t.Fatal("an empty hold names an earliest update")
	}
	held.add(albumItem(1, 10, "later", ""), 7, now)
	held.add(albumItem(2, 20, "sooner", ""), 5, now)
	held.add(albumItem(1, 11, "later", ""), 8, now)
	if first, ok := held.earliest(); !ok || first != 5 {
		t.Fatalf("earliest = %d, %v; want 5, the first update of either album", first, ok)
	}
	held.inChat(2)
	if first, _ := held.earliest(); first != 7 {
		t.Errorf("earliest = %d after the first album was taken, want 7", first)
	}
}

// Albums in two chats may share an id; each is its own album.
func TestAlbumIDsAreScopedToTheirChat(t *testing.T) {
	held := newAlbums()
	now := time.Unix(1_000, 0)
	held.add(albumItem(1, 10, "g", ""), 0, now)
	held.add(albumItem(2, 10, "g", ""), 0, now)
	if got := held.settled(now.Add(albumSettle)); len(got) != 2 {
		t.Fatalf("settled %d albums, want 2", len(got))
	}
}

func TestAnAlbumIsOneMessageWithEveryFile(t *testing.T) {
	bot := &poller{}
	reply := &tgMsgAlias{MessageID: 3}
	captioned := albumItem(1, 11, "g", "both of these")
	captioned.ReplyToMessage = reply
	whole := &album{items: []*tgMsgAlias{albumItem(1, 12, "g", " and this one "), captioned, albumItem(1, 10, "g", "")}}
	message, attachments := whole.asOneMessage(bot)
	if message.MessageID != 10 {
		t.Errorf("the message carries id %d, want the first item's, 10", message.MessageID)
	}
	if want := "both of these\nand this one"; message.Caption != want {
		t.Errorf("the message's words are %q, want every caption in the order sent, %q", message.Caption, want)
	}
	if message.ReplyToMessage != reply {
		t.Error("the message lost the reply another item carried")
	}
	var names []string
	for _, attachment := range attachments {
		names = append(names, attachment.Name)
	}
	want := []string{"photo-10.jpg", "photo-11.jpg", "photo-12.jpg"}
	if !slices.Equal(names, want) {
		t.Errorf("attachments = %v, want %v in the order they were sent", names, want)
	}
	if whole.items[0].MessageID != 12 || whole.items[1].Caption != "both of these" || whole.items[2].Caption != "" || whole.items[0].Caption != " and this one " {
		t.Error("asOneMessage changed the items it was given")
	}
}

// Items from two senders never join one message, even under one album id
// in one chat: each sender's items are an album of their own, checked
// against the allowlist on their own.
func TestTwoSendersNeverShareAnAlbum(t *testing.T) {
	held := newAlbums()
	now := time.Unix(1_000, 0)
	mine := albumItem(1, 10, "g", "mine")
	mine.From = &User{ID: 7}
	theirs := albumItem(1, 11, "g", "theirs")
	theirs.From = &User{ID: 8}
	onBehalf := albumItem(1, 12, "g", "a channel's")
	onBehalf.From = &User{ID: 7}
	onBehalf.SenderChat = &Chat{ID: -100}
	held.add(mine, 0, now)
	held.add(theirs, 0, now)
	held.add(onBehalf, 0, now)
	got := held.settled(now.Add(albumSettle))
	if len(got) != 3 {
		t.Fatalf("settled %d albums, want one per sender", len(got))
	}
	for _, whole := range got {
		if len(whole.items) != 1 {
			t.Errorf("an album holds %d items from different senders", len(whole.items))
		}
	}
}
