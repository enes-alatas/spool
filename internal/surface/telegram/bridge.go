// Package telegram bridges Spool loops to Telegram: one bot per loop, all in
// a shared group. Bots cannot see other bots' messages (platform rule), so
// loop-to-loop delivery is always internal; Telegram is a mirror plus the
// human I/O surface.
package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

const (
	pollTimeoutSec = 50
	maxMsgLen      = 4096
	sendSpacing    = time.Second // per-bot pacing (Telegram: ~1 msg/s)
	dedupSize      = 512
	// sendAttempts and sendBackoff bound the retry a failed send gets: a
	// timeout against api.telegram.org is ordinary, and one attempt made it
	// cost the message (#147). Four attempts over ~7s of backoff outlast a
	// blip without holding the bot's paced queue for a minute.
	sendAttempts = 4
	sendBackoff  = time.Second
	// defaultBindSettle is how long after binding a bot waits before it may win a
	// group's ingest election (ADR-0020): margin for clock skew between
	// Telegram's message dates and ours. It is the default rather than a
	// constant of the bridge, because a test driving a stand-in API has no
	// skew to cover and would otherwise sleep the margin out on every run.
	defaultBindSettle = 5 * time.Second
)

type Bridge struct {
	// bindSettle is this bridge's ingest-election margin, defaultBindSettle
	// unless SetBindSettle shortened it for a test.
	bindSettle time.Duration

	store   store.Store
	bus     *bus.Bus
	router  *route.Router
	log     *slog.Logger
	apiBase string

	ctx context.Context

	mu      sync.Mutex
	pollers map[string]*poller // loop ID → poller
	dedup   *dedupLRU

	pairMu       sync.Mutex
	pairNotified map[int64]bool // senders already told their pairing code this run
	// notOwnerNotified tracks which (loop, private chat) pairs have been
	// told that the loop answers only its owner, so each bot says it once
	// per run. Keyed by loop as well as chat: a private chat's id is the
	// human's user id in every bot's numbering, so a fleet-wide key would
	// let the first bot's notice silence all the others.
	notOwnerNotified map[string]bool
}

// NewBridge builds the bridge. apiBase is the Bot API to talk to; "" means
// the live one.
func NewBridge(st store.Store, b *bus.Bus, r *route.Router, log *slog.Logger, apiBase string) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{store: st, bus: b, router: r, log: log, apiBase: apiBase,
		bindSettle: defaultBindSettle,
		pollers:    map[string]*poller{}, dedup: newDedupLRU(dedupSize),
		pairNotified: map[int64]bool{}, notOwnerNotified: map[string]bool{}}
}

// SetBindSettle overrides the ingest-election margin, and must be called
// before Start: the pollers read it as they run. Only a test harness pointed
// at a stand-in API should call it at all — against the real Telegram the
// margin is what keeps a newly bound bot from duplicating what the incumbent
// already ingested. A value of 0 or less keeps the default.
func (br *Bridge) SetBindSettle(d time.Duration) {
	if d > 0 {
		br.bindSettle = d
	}
}

// Start launches pollers for every configured loop and the mirror consumer.
func (br *Bridge) Start(ctx context.Context) {
	br.ctx = ctx
	loops, err := br.store.Loops().List(ctx)
	if err != nil {
		br.log.Error("telegram: list loops", "err", err)
		return
	}
	for _, l := range loops {
		if l.TGBotToken != "" && l.Status != store.StatusArchived {
			br.startPoller(l)
		}
	}
	go br.mirror(ctx)
}

// --- surface.Surface interface ---

// ValidateCredential resolves a bot token to its bot username.
func (br *Bridge) ValidateCredential(ctx context.Context, token string) (string, error) {
	u, err := NewClientAt(br.apiBase, token).GetMe(ctx)
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// LoopChanged brings the loop's poller in line with its stored configuration.
// The row is read here rather than handed in: the hub says only that a loop
// changed, and which of its fields matter is the bridge's own business — so
// this is called for every edit, and answering "nothing to do" is part of the
// job. It has to be: a restart costs a replay. getUpdates offsets are held by
// the poller, so a new one starts at 0 and Telegram re-sends everything it
// still holds, for the message key and the dedup LRU to throw away again.
func (br *Bridge) LoopChanged(ctx context.Context, loopID string) {
	l, err := br.store.Loops().Get(ctx, loopID)
	if err != nil {
		br.log.Error("telegram: read changed loop", "loop", loopID, "err", err)
		return
	}
	if l.TGBotToken == "" || l.Status == store.StatusArchived {
		br.stopPoller(loopID)
		return
	}
	if p := br.poller(loopID); p != nil && p.token == l.TGBotToken {
		return // same bot, still polling: the edit was none of our business
	}
	br.stopPoller(loopID)
	br.startPoller(l)
}

func (br *Bridge) LoopRemoved(loopID string) { br.stopPoller(loopID) }

func (br *Bridge) Status(loopID string) any {
	br.mu.Lock()
	defer br.mu.Unlock()
	p, ok := br.pollers[loopID]
	if !ok {
		return map[string]any{"polling": false}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return map[string]any{
		"polling":        true,
		"last_update_at": p.lastUpdate,
		"last_error":     p.lastError,
	}
}

// --- pollers ---

type poller struct {
	loopID string
	name   string
	// token is the bot this poller was started for, so LoopChanged can tell
	// a bot swap — which needs a new poller — from an edit that left the
	// bot alone.
	token  string
	client *Client
	cancel context.CancelFunc
	sendCh chan sendReq

	mu         sync.Mutex
	lastUpdate int64
	lastError  string
}

type sendReq struct {
	chatID int64
	text   string
	// replyTo is this bot's own id for the message being replied to, or 0
	// for a plain post. A foreign bot's id is never passed here: message_id
	// is numbered per bot conversation (ADR-0020).
	replyTo int64
	// recordFor is the internal message whose surface id this send mints;
	// 0 when the send is not worth referencing later.
	recordFor int64
}

func (br *Bridge) startPoller(l *store.Loop) {
	ctx, cancel := context.WithCancel(br.ctx)
	p := &poller{
		loopID: l.ID,
		name:   l.Name,
		token:  l.TGBotToken,
		client: NewClientAt(br.apiBase, l.TGBotToken),
		cancel: cancel,
		sendCh: make(chan sendReq, 128),
	}
	br.mu.Lock()
	br.pollers[l.ID] = p
	br.mu.Unlock()
	go br.pollLoop(ctx, p)
	go br.sendLoop(ctx, p)
	br.log.Info("telegram poller started", "loop", l.Name, "bot", l.TGBotUsername)
}

func (br *Bridge) stopPoller(loopID string) {
	br.mu.Lock()
	p, ok := br.pollers[loopID]
	if ok {
		delete(br.pollers, loopID)
	}
	br.mu.Unlock()
	if ok {
		p.cancel()
	}
}

func (br *Bridge) pollLoop(ctx context.Context, p *poller) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := p.client.GetUpdates(ctx, offset, pollTimeoutSec)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.mu.Lock()
			p.lastError = err.Error()
			p.mu.Unlock()
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				switch apiErr.Code {
				case 401:
					br.log.Error("telegram token invalid; poller stopped", "loop", p.name)
					return
				case 409:
					br.log.Error("telegram 409: another getUpdates consumer for this bot; poller paused 60s", "loop", p.name)
					sleepCtx(ctx, time.Minute)
					continue
				case 429:
					sleepCtx(ctx, time.Duration(max(apiErr.RetryAfter, 1))*time.Second)
					continue
				}
			}
			sleepCtx(ctx, backoff)
			backoff = minDur(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		p.mu.Lock()
		p.lastError = ""
		p.lastUpdate = time.Now().UnixMilli()
		p.mu.Unlock()
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message != nil {
				br.handleMessage(ctx, p, u.Message)
			}
		}
	}
}

func (br *Bridge) handleMessage(ctx context.Context, p *poller, m *tgMsgAlias) {
	if m.From == nil || m.From.IsBot {
		return // defensive: Telegram shouldn't deliver bot messages at all
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}
	author := "someone"
	if m.From.Username != "" {
		author = m.From.Username
	} else if m.From.FirstName != "" {
		author = m.From.FirstName
	}

	isGroup := m.Chat.Type == "group" || m.Chat.Type == "supergroup"

	// Access gate: only allowlisted senders reach loops or affect state.
	// Unknown senders get a pending record + pairing code; strangers can't
	// bind groups, trigger /spool_status, or message loops.
	if !br.senderAllowed(ctx, p, m, author, isGroup) {
		return
	}

	if isGroup {
		br.maybeBindGroup(ctx, p, m.Chat.ID)
	}

	// /spool_status works even with bot privacy mode on and forces binding
	if strings.HasPrefix(text, "/spool_status") {
		br.replyStatus(ctx, p, m.Chat.ID)
		return
	}

	// Every bot that saw the message records its own id for it, whether or
	// not it is the one that ingests it. That sighting is what later lets
	// this bot's reply thread under the message: it cannot borrow the
	// ingesting bot's id, which belongs to another numbering (ADR-0020).
	br.recordSighting(ctx, p, m)

	if isGroup {
		// Every bot in the group sees this message under its own message_id,
		// so exactly one of them may persist it.
		if br.groupIngestLoopID(ctx, m.Chat.ID, m.Date) != p.loopID {
			return
		}
		if !br.dedup.Add(dedupKey(p.loopID, m.Chat.ID, m.MessageID)) {
			return
		}
		err := br.router.Ingest(ctx, route.InboundMessage{
			Origin:      store.OriginTelegramGroup,
			Author:      author,
			Text:        text,
			TGChatID:    m.Chat.ID,
			TGMessageID: m.MessageID,
			TGBotLoopID: p.loopID,
			TGKey:       tgKey(m),
			ReplyToID:   br.inboundReplyTarget(ctx, p, m),
		})
		if err != nil && !errors.Is(err, store.ErrDuplicate) {
			br.log.Error("telegram group ingest", "err", err)
		}
		return
	}

	if m.Chat.Type == "private" {
		br.maybeCaptureOwnerDM(ctx, p, m)
		if !br.ownerOf(ctx, p, m.From.ID) {
			br.logTurnedAway(p, m, author, "sender is not this loop's owner")
			// Not the loop's owner. Delivering this would leave the loop
			// unable to answer — owner_dm addresses the owner, so the reply
			// would land in someone else's chat. Until non-owner private
			// conversations are designed, say so instead of going silent
			// (ADR-0026 amendment).
			br.notifyNotOwner(ctx, p, m.Chat.ID)
			return
		}
		if !br.dedup.Add(dedupKey(p.loopID, m.Chat.ID, m.MessageID)) {
			return
		}
		err := br.router.Ingest(ctx, route.InboundMessage{
			Origin:      store.OriginTelegramDM,
			Author:      author,
			Text:        text,
			TGChatID:    m.Chat.ID,
			TGMessageID: m.MessageID,
			TGBotLoopID: p.loopID,
			TGKey:       tgKey(m),
			ReplyToID:   br.inboundReplyTarget(ctx, p, m),
			ImplicitTo:  p.loopID,
		})
		if err != nil && !errors.Is(err, store.ErrDuplicate) {
			br.log.Error("telegram dm ingest", "err", err)
		}
	}
}

// groupIngestLoopID names the one bot allowed to ingest chatID's messages,
// as of the message Telegram dated at msgDate. Telegram hands each bot its
// own message_id for the same human message, so no key computed from an
// update can tell "the same message twice" from "two messages" — the only
// reliable dedup is to let a single poller through.
//
// The election is the lowest loop ID among the bots polling that group whose
// binding predates the message. That second clause is what makes every
// poller agree on one answer: a bot binds to a group in the middle of
// handling a message, so a candidate set read as "whoever is bound right
// now" differs between pollers racing on the same message. A message dated
// after a committed bind, by contrast, was received after that bind — so
// every poller handling it reads the same set, whatever order they run in,
// and a bot that joins a live group (or is catching up on a backlog) leaves
// the incumbent to finish the messages that predate it.
//
// While the elected poller is down but still registered — a 409 pause, say —
// the group is deaf: nobody steps in, because stepping in on a live poller's
// behalf is exactly the double-ingest this prevents. Returns "" when nobody
// is eligible (a brand-new group, where the first message is what binds the
// bots); the caller drops the message rather than let every bot ingest it.
func (br *Bridge) groupIngestLoopID(ctx context.Context, chatID, msgDate int64) string {
	loops, err := br.store.Loops().List(ctx)
	if err != nil {
		br.log.Error("telegram: group ingest election", "err", err)
		return ""
	}
	br.mu.Lock()
	defer br.mu.Unlock()
	ingest := ""
	for _, l := range loops {
		if l.TGGroupChatID != chatID || l.Status == store.StatusArchived {
			continue
		}
		if !boundBefore(l, msgDate, br.bindSettle) {
			continue
		}
		if _, polling := br.pollers[l.ID]; !polling {
			continue
		}
		if ingest == "" || l.ID < ingest {
			ingest = l.ID
		}
	}
	return ingest
}

// boundBefore reports whether l's bot was bound to its group early enough to
// ingest a message Telegram dated at msgDate. The settle margin covers the skew
// between Telegram's clock and ours: erring long only delays a newcomer's
// first ingest by a few seconds, while erring short would let it duplicate
// what the incumbent already took. A binding from before this rule existed
// is recorded as 0 and always qualifies.
func boundBefore(l *store.Loop, msgDate int64, settle time.Duration) bool {
	if l.TGGroupBoundAt == 0 {
		return true
	}
	return msgDate > l.TGGroupBoundAt/1000+int64(settle.Seconds())
}

// dedupKey identifies a telegram message: the chat, the id, and the bot that
// numbered it. It guards a poller re-reading its own updates; cross-bot
// duplicates are prevented upstream, by only one bot ingesting a group.
func dedupKey(loopID string, chatID, messageID int64) string {
	return fmt.Sprintf("%s:%d:%d", loopID, chatID, messageID)
}

// tgKey identifies a telegram message by what every bot observing it sees
// alike: the chat, the sender, Telegram's own date, and the text. Bots agree
// on all four while disagreeing on message_id, so it is the only join
// between one bot's sighting and another's ingested row.
func tgKey(m *tgMsgAlias) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%d|%d|%s", m.Chat.ID, m.From.ID, m.Date, m.Text))
	return hex.EncodeToString(sum[:16])
}

// recordSighting stores this bot's own id for a message it received —
// every inbound message, group or DM, since a DM's only reference is the
// receiving bot's sighting of it.
func (br *Bridge) recordSighting(ctx context.Context, p *poller, m *tgMsgAlias) {
	err := br.store.Messages().RecordSighting(ctx, tgKey(m), p.loopID, m.Chat.ID, m.MessageID, time.Now().UnixMilli())
	if err != nil {
		br.log.Warn("telegram: record sighting", "loop", p.name, "err", err)
	}
}

// recordSentRef maps an internal message to the id Telegram minted for it in
// this bot's numbering, so a later reply can target it.
func (br *Bridge) recordSentRef(ctx context.Context, p *poller, req sendReq, sent *Message) {
	if req.recordFor == 0 || sent == nil || sent.MessageID == 0 {
		return
	}
	err := br.store.Messages().PutRef(ctx, &store.SurfaceRef{
		MessageID: req.recordFor, BotLoopID: p.loopID,
		TGChatID: req.chatID, TGMessageID: sent.MessageID,
	})
	if err != nil {
		br.log.Warn("telegram: record sent reference", "loop", p.name, "err", err)
	}
}

// inboundReplyTarget identifies the message a human's native reply points
// at. Telegram's embedded reply_to_message carries an id in the receiving
// bot's own numbering, so it resolves directly only for messages that bot
// sent or saw; for another loop's post — which no other bot ever receives —
// what is left to identify it by is the embedded copy's sender and text.
//
// The sender is the stronger half: a bot's username names the loop that
// posted, and the ingesting bot is elected by loop id rather than by
// authorship (ADR-0020), so most group replies arrive at a bot that is not
// the author's. Knowing the author first is what makes the text lookup
// safe — among two loops that posted the same words it no longer has to
// guess, because only one of them is the one being answered.
//
// The text needs undoing first. A loop's reply to another loop's post goes
// out with a quoted line prepended (ADR-0025 amendment), and Telegram
// embeds a message as it was sent, so the copy carries a line the stored
// row does not. A target that resolves to nothing stays 0: the message is
// delivered as an ordinary one rather than aimed at a guess.
func (br *Bridge) inboundReplyTarget(ctx context.Context, p *poller, m *tgMsgAlias) int64 {
	rm := m.ReplyToMessage
	if rm == nil || rm.From == nil {
		return 0
	}
	msgs := br.store.Messages()
	if target, err := msgs.ByRef(ctx, p.loopID, m.Chat.ID, rm.MessageID); err == nil {
		return target.ID
	}
	if !rm.From.IsBot {
		if target, err := msgs.ByTGKey(ctx, tgKey(rm)); err == nil {
			return target.ID
		}
		return 0
	}
	author := br.loopIDOfBot(ctx, rm.From.Username)
	if author == "" || rm.Text == "" {
		return 0
	}
	for _, text := range []string{rm.Text, withoutQuotePrefix(rm.Text)} {
		if target, err := msgs.LatestGroupPostBy(ctx, author, text); err == nil {
			return target.ID
		}
	}
	return 0
}

// loopIDOfBot names the loop whose bot posts under username, or "" for a
// bot that is not one of ours. Telegram gives usernames without the @ and
// treats them case-insensitively.
func (br *Bridge) loopIDOfBot(ctx context.Context, username string) string {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return ""
	}
	loops, err := br.store.Loops().List(ctx)
	if err != nil {
		br.log.Error("telegram: reply author lookup", "err", err)
		return ""
	}
	for _, l := range loops {
		if strings.EqualFold(strings.TrimPrefix(l.TGBotUsername, "@"), username) {
			return l.ID
		}
	}
	return ""
}

// withoutQuotePrefix removes the line render prepends when it cannot anchor
// a reply natively, returning what the store holds. Text that carries no
// such line comes back unchanged, so the caller can try both.
func withoutQuotePrefix(text string) string {
	if !strings.HasPrefix(text, quoteMark) {
		return text
	}
	if i := strings.Index(text, "\n\n"); i >= 0 {
		return text[i+2:]
	}
	return text
}

// render prepares a loop's message for one chat: the native reply anchor
// when the sending bot holds its own id for the target, and otherwise a
// quoted first line naming what the message answers. A bot holds no id for
// another loop's post — bots never receive each other's messages — so
// without the quote a reply would read as an unrelated remark (ADR-0025,
// amendment). A foreign bot's id is never used as an anchor: message_id is
// numbered per bot conversation (ADR-0020).
func (br *Bridge) render(ctx context.Context, mp *route.MessagePayload, chatID int64) (anchor int64, text string) {
	if mp.ReplyToID == 0 {
		return 0, mp.Text
	}
	if ref, err := br.store.Messages().Ref(ctx, mp.ReplyToID, mp.FromLoopID); err == nil && ref.TGChatID == chatID {
		return ref.TGMessageID, mp.Text
	}
	target, err := br.store.Messages().Get(ctx, mp.ReplyToID)
	if err != nil {
		return 0, mp.Text
	}
	return 0, quotePrefix(target) + mp.Text
}

// quoteLen caps the quoted line; long enough to identify the message, short
// enough that the reply itself stays the message.
const quoteLen = 80

// quoteMark opens the quoted line, and is how an inbound copy of one is
// recognised again.
const quoteMark = "↳ re "

// quotePrefix renders the one line that stands in for a native reply.
func quotePrefix(target *store.Message) string {
	quoted := strings.Join(strings.Fields(target.Text), " ")
	return fmt.Sprintf("%s%s: %s\n\n", quoteMark, target.Author, excerpt(quoted, quoteLen))
}

// tgMsgAlias keeps handleMessage readable without exporting internals.
type tgMsgAlias = Message

// senderAllowed enforces the allowlist. Unknown senders are registered as
// pending with a pairing code; on DM they are told the code once per run.
func (br *Bridge) senderAllowed(ctx context.Context, p *poller, m *tgMsgAlias, author string, isGroup bool) bool {
	sender, err := br.store.TGSenders().Get(ctx, m.From.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		br.log.Error("sender lookup", "err", err)
		br.logTurnedAway(p, m, author, "sender lookup failed")
		return false // fail closed
	}

	if errors.Is(err, store.ErrNotFound) {
		via := "dm:" + p.name
		if isGroup {
			via = "group:" + p.name
		}
		now := time.Now().UnixMilli()
		sender = &store.TGSender{
			TGUserID:     m.From.ID,
			Username:     m.From.Username,
			Display:      strings.TrimSpace(m.From.FirstName),
			Status:       store.SenderPending,
			PairCode:     pairCode(),
			FirstSeenVia: via,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if cerr := br.store.TGSenders().Create(ctx, sender); cerr != nil {
			if !errors.Is(cerr, store.ErrDuplicate) {
				br.log.Error("sender create", "err", cerr)
				br.logTurnedAway(p, m, author, "sender could not be registered")
				return false
			}
			// another poller registered them first; re-read
			if sender, err = br.store.TGSenders().Get(ctx, m.From.ID); err != nil {
				br.logTurnedAway(p, m, author, "sender registered by another poller but unreadable")
				return false
			}
		} else {
			br.log.Info("new telegram sender pending approval", "user", author, "id", m.From.ID)
			br.bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender})
		}
	}

	switch sender.Status {
	case store.SenderAllowed:
		return true
	case store.SenderBlocked:
		br.logTurnedAway(p, m, author, "sender is blocked")
		return false
	default: // pending
		if !isGroup {
			br.pairMu.Lock()
			notified := br.pairNotified[m.From.ID]
			br.pairNotified[m.From.ID] = true
			br.pairMu.Unlock()
			if !notified {
				p.enqueueSend(m.Chat.ID, fmt.Sprintf(
					"Spool: you're not authorized yet. Your pairing code is %s — ask the operator to approve you in the Spool control room (Access page).",
					sender.PairCode))
			}
		}
		br.logTurnedAway(p, m, author, "sender is pending approval")
		return false
	}
}

// logTurnedAway records an inbound message that reached a loop's bot and was
// then discarded. Every one of these is a person whose words went nowhere and
// who has no way to tell: the poller advances its offset past a message
// whether or not anything was done with it, so a drop here is permanent. The
// reason is the point — without it the only evidence is a line that never
// appears, and a reader has to infer the branch from its absence (#161).
//
// Info rather than Debug: these are rare, human-caused, and each one is a
// message that will not arrive. The routine returns are deliberately not
// logged — losing a group ingest election or a dedup hit means another bot
// took the message, not that it was lost.
func (br *Bridge) logTurnedAway(p *poller, m *tgMsgAlias, author, reason string) {
	br.log.Info("telegram inbound discarded", "loop", p.name, "reason", reason,
		"author", author, "chat_type", m.Chat.Type, "chat_id", m.Chat.ID,
		"message_id", m.MessageID)
}

const pairAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func pairCode() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "ERRTRY"
	}
	for i := range b {
		b[i] = pairAlphabet[int(b[i])%len(pairAlphabet)]
	}
	return string(b[:])
}

func (br *Bridge) maybeBindGroup(ctx context.Context, p *poller, chatID int64) {
	l, err := br.store.Loops().Get(ctx, p.loopID)
	if err != nil || l.TGGroupChatID == chatID {
		return
	}
	boundAt := time.Now().UnixMilli()
	if err := br.store.Loops().SetGroupBinding(ctx, l.ID, chatID, boundAt, boundAt); err != nil {
		br.log.Error("group bind", "err", err)
		return
	}
	br.log.Info("telegram group bound", "loop", l.Name, "chat_id", chatID)
	br.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: l.ID, Payload: map[string]any{
		"loop_id": l.ID, "name": l.Name, "tg_group_bound": true,
	}})
}

// maybeCaptureOwnerDM records where this loop's bot can write privately to
// its configured owner. A bot cannot open a private chat, so the address
// only exists once the owner has written to this bot at least once — and it
// is that bot's chat, not another's (#73). Only the configured owner's chat
// is captured: being allowlisted, or messaging first, does not make someone
// the owner.
func (br *Bridge) maybeCaptureOwnerDM(ctx context.Context, p *poller, m *tgMsgAlias) {
	l, err := br.store.Loops().Get(ctx, p.loopID)
	if err != nil || l.OwnerTGUserID == 0 || l.OwnerTGUserID != m.From.ID {
		return
	}
	if l.OwnerDMChatID == m.Chat.ID {
		return
	}
	if err := br.store.Loops().SetOwnerDMChat(ctx, l.ID, m.Chat.ID, time.Now().UnixMilli()); err != nil {
		br.log.Error("owner dm capture", "loop", l.Name, "err", err)
		return
	}
	br.log.Info("owner dm captured", "loop", l.Name, "chat_id", m.Chat.ID)
	br.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: l.ID, Payload: map[string]any{
		"loop_id": l.ID, "name": l.Name, "owner_dm_ready": true,
	}})
}

// ownerOf reports whether tgUserID is the loop's configured owner.
func (br *Bridge) ownerOf(ctx context.Context, p *poller, tgUserID int64) bool {
	l, err := br.store.Loops().Get(ctx, p.loopID)
	return err == nil && l.OwnerTGUserID != 0 && l.OwnerTGUserID == tgUserID
}

// notifyNotOwner tells a non-owner, once per loop per run, why their DM
// goes unanswered. The same pacing as the pairing code — a turned away
// sender should learn the reason, not be answered on every message — but
// per loop, because each one answers a different owner.
func (br *Bridge) notifyNotOwner(ctx context.Context, p *poller, chatID int64) {
	key := fmt.Sprintf("%s:%d", p.loopID, chatID)
	br.pairMu.Lock()
	notified := br.notOwnerNotified[key]
	br.notOwnerNotified[key] = true
	br.pairMu.Unlock()
	if notified {
		return
	}
	owner := "its owner"
	if l, err := br.store.Loops().Get(ctx, p.loopID); err == nil && l.OwnerTGUserID != 0 {
		if s, err := br.store.TGSenders().Get(ctx, l.OwnerTGUserID); err == nil && s.Username != "" {
			owner = "@" + s.Username
		}
	}
	p.enqueueSend(chatID, fmt.Sprintf(
		"Spool: %s reads direct messages only from %s for now. Reach it in the group instead.",
		p.name, owner))
}

func (br *Bridge) replyStatus(ctx context.Context, p *poller, chatID int64) {
	l, err := br.store.Loops().Get(ctx, p.loopID)
	if err != nil {
		return
	}
	next := "none scheduled"
	if e, err := br.store.Schedule().Get(ctx, l.ID); err == nil && e.NextTickAt > 0 {
		next = time.UnixMilli(e.NextTickAt).UTC().Format("15:04 UTC")
	}
	p.enqueueSend(chatID, fmt.Sprintf("spool: loop %q is %s · next tick %s", l.Name, l.Status, next))
}

// --- outbound: per-bot paced sender ---

func (p *poller) enqueueSend(chatID int64, text string) {
	p.enqueue(sendReq{chatID: chatID, text: text})
}

// enqueue splits a send into Telegram-sized chunks. Only the first chunk
// carries the reply anchor and mints the message's surface reference: the
// continuation chunks are the same message, not new targets.
func (p *poller) enqueue(req sendReq) {
	for i, chunk := range splitMessage(req.text, maxMsgLen) {
		part := sendReq{chatID: req.chatID, text: chunk}
		if i == 0 {
			part.replyTo, part.recordFor = req.replyTo, req.recordFor
		}
		select {
		case p.sendCh <- part:
		default: // queue full: drop rather than block the bridge
		}
	}
}

func (br *Bridge) sendLoop(ctx context.Context, p *poller) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-p.sendCh:
			br.sendWithRetries(ctx, p, req)
			sleepCtx(ctx, sendSpacing)
		}
	}
}

// sendWithRetries makes a send survive the ordinary failure — a timeout or a
// 5xx against api.telegram.org — instead of costing the message. A rejection
// Telegram means (a 4xx that is not 429) is not retried: sending it again
// would fail the same way, slower.
//
// A send that runs out of attempts is recorded rather than only logged: on
// the message, so the record answers "did that reach them", and as a spool
// event, so the loop's timeline shows the gap where its words should be.
// Nothing here is silent (#147) — except a cancelled context, which means the
// process is going away and there is no live context left to record through.
func (br *Bridge) sendWithRetries(ctx context.Context, p *poller, req sendReq) {
	var lastErr error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		sent, err := p.client.SendMessage(ctx, req.chatID, req.text, req.replyTo)
		if err == nil {
			br.recordSentRef(ctx, p, req, sent)
			br.recordSendResult(ctx, req, nil)
			return
		}
		lastErr = err
		var apiErr *APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.Code == 429:
			// Telegram says how long to wait, and means it — but only when
			// there is an attempt left to spend it on. retry_after runs to
			// tens of seconds and sendLoop is serial per bot, so waiting
			// after the last attempt holds the whole queue for nothing.
			if attempt < sendAttempts-1 {
				sleepCtx(ctx, time.Duration(max(apiErr.RetryAfter, 1))*time.Second)
			}
		case errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500:
			br.failSend(ctx, p, req, err, attempt+1)
			return
		default:
			if attempt < sendAttempts-1 {
				br.log.Warn("telegram send failed; retrying",
					"loop", p.name, "attempt", attempt+1, "err", err)
				sleepCtx(ctx, sendBackoff<<attempt)
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
	br.failSend(ctx, p, req, lastErr, sendAttempts)
}

// failSend gives up on a send and leaves the evidence in the two places
// somebody would look: the message row and the loop's timeline.
func (br *Bridge) failSend(ctx context.Context, p *poller, req sendReq, err error, attempts int) {
	br.log.Error("telegram send failed; giving up",
		"loop", p.name, "chat", req.chatID, "attempts", attempts, "err", err)
	br.recordSendResult(ctx, req, err)
	e := &store.Event{
		LoopID:  p.loopID,
		TS:      time.Now().UnixMilli(),
		Type:    "spool",
		Subtype: "send_failed",
		Payload: fmt.Sprintf(`{"chat":%q,"attempts":%d,"error":%q,"text":%q}`,
			br.chatName(ctx, p, req.chatID), attempts, err.Error(), excerpt(req.text, excerptLen)),
	}
	if _, insErr := br.store.Events().Insert(ctx, e); insErr != nil {
		br.log.Error("telegram: record send failure", "loop", p.name, "err", insErr)
		return
	}
	br.bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: p.loopID, Payload: e})
}

// chatName says which conversation a send was aimed at, in the operator's
// terms rather than Telegram's. A chat id names nothing a reader knows; what
// they need from a lost message is who never heard it.
func (br *Bridge) chatName(ctx context.Context, p *poller, chatID int64) string {
	l, err := br.store.Loops().Get(ctx, p.loopID)
	if err != nil {
		// The operator reads "a chat" either way, but a store that cannot be
		// read during a send failure is a second problem, not a naming one.
		br.log.Warn("telegram: name chat for send failure", "loop", p.name, "err", err)
		return "a chat"
	}
	switch chatID {
	case l.TGGroupChatID:
		return "the group"
	case l.OwnerDMChatID:
		return "the owner"
	default:
		return "a chat"
	}
}

// recordSendResult marks the message this send carried: a failure with its
// error, a success by resolving whatever failure the row already carried.
//
// A success does not erase send_failed_at. The row did fail, the loop's
// timeline says so (#147), and a store that quietly disagreed with its own
// event would be the harder bug. Resolving instead takes it out of the
// operator's undelivered count and off the Undelivered tab, which is what
// "the retry worked" actually means to them (#269).
func (br *Bridge) recordSendResult(ctx context.Context, req sendReq, err error) {
	if req.recordFor == 0 {
		return
	}
	if err == nil {
		now := time.Now().UnixMilli()
		// Called on every success, including a first attempt that never
		// failed: ResolveSend is a no-op unless the row carries an
		// unresolved failure, so the caller does not need to know which
		// kind of success this was.
		if _, resErr := br.store.Messages().ResolveSend(ctx, req.recordFor, now,
			store.SendResolutionDelivered, 0); resErr != nil {
			br.log.Warn("telegram: resolve send failure", "err", resErr)
		}
		// And the failures these words were said again for, if the loop
		// said this message was a resend — the one it named, and anything
		// that one resent before it. Only on success, and read from the
		// row rather than from this send: a resend that failed too is
		// itself resent later, and the chain is what lets that last send
		// close the failure this one could not (#270).
		if _, resErr := br.store.Messages().ResolveResends(ctx, req.recordFor, now); resErr != nil {
			br.log.Warn("telegram: resolve resent failures", "err", resErr)
		}
		return
	}
	if setErr := br.store.Messages().SetSendResult(ctx, req.recordFor,
		time.Now().UnixMilli(), err.Error()); setErr != nil {
		br.log.Warn("telegram: record send result", "err", setErr)
	}
}

// excerptLen caps the excerpt of a lost message an event payload carries:
// enough to recognise which message it was, not the message over again.
const excerptLen = 200

// excerpt keeps the opening of s, within n bytes. The head, because a message
// is recognised by how it starts — the same choice quotePrefix and prompt.go's
// truncate make. The cut walks back to a rune boundary: slicing bytes at an
// arbitrary offset halves a multi-byte character, and the note then begins in
// a stray continuation byte.
func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// --- mirroring ---

// mirror consumes message bus items and applies the mirror rules.
func (br *Bridge) mirror(ctx context.Context) {
	// Both kinds, one path: a retry (#269) is the same send of the same row,
	// asked for by the operator instead of by the loop, and anything the
	// mirror rules decide about a message must decide the same way twice.
	items, cancel := br.bus.Subscribe(func(i bus.Item) bool {
		return i.Kind == bus.KindMessage || i.Kind == bus.KindSendRetry
	})
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-items:
			if !ok {
				return
			}
			mp, ok := item.Payload.(*route.MessagePayload)
			if !ok {
				continue
			}
			br.mirrorMessage(ctx, mp)
		}
	}
}

func (br *Bridge) mirrorMessage(ctx context.Context, mp *route.MessagePayload) {
	// Only a loop's words leave the hub. Telegram-origin messages are
	// already visible in telegram, and nothing the operator writes in the
	// control room is mirrored outward — not to the group, not anywhere
	// (ADR-0032 item 4). That is the operator's security posture rather
	// than a gap: Spool holds no means of posting his words on a third
	// party, so a bug here cannot become a message sent as him. The test
	// is on the origin, not the conversation, so a destination added later
	// inherits the rule instead of having to remember it.
	if mp.Origin != store.OriginLoop {
		return
	}
	p := br.poller(mp.FromLoopID)
	switch mp.Conversation {
	case store.ConversationGroup:
		// a loop's explicit group send: post to its bound group as its
		// own bot. No poller just means the loop has no bot — normal
		// for a fleet without telegram.
		if p == nil {
			return
		}
		l, err := br.store.Loops().Get(ctx, mp.FromLoopID)
		if err != nil || l.TGGroupChatID == 0 {
			return
		}
		anchor, text := br.render(ctx, mp, l.TGGroupChatID)
		p.enqueue(sendReq{chatID: l.TGGroupChatID, text: text, replyTo: anchor, recordFor: mp.ID})
	case store.ConversationOwnerDM:
		// a loop's owner_dm send: deliver to the chat route.Send pinned
		// at send time — never re-resolved here, so a DM arriving
		// between send and delivery cannot redirect it. route.Send
		// refuses when no chat resolves, and a captured chat implies
		// the loop had a bot — so a miss on either here is an internal
		// fault, not a model error, and must not drop the private
		// message silently.
		if p == nil {
			br.log.Error("owner dm delivery: loop has no bot", "loop", mp.FromLoopID)
			return
		}
		if mp.OwnerDMChat == 0 {
			br.log.Error("owner dm delivery: send carried no pinned chat", "loop", mp.FromLoopID)
			return
		}
		anchor, text := br.render(ctx, mp, mp.OwnerDMChat)
		p.enqueue(sendReq{chatID: mp.OwnerDMChat, text: text, replyTo: anchor, recordFor: mp.ID})
	}
	// control_room lives in the web UI alone; telegram sees nothing
}

func (br *Bridge) poller(loopID string) *poller {
	br.mu.Lock()
	defer br.mu.Unlock()
	return br.pollers[loopID]
}

// --- small utils ---

type dedupLRU struct {
	mu    sync.Mutex
	max   int
	seen  map[string]bool
	order []string
}

func newDedupLRU(max int) *dedupLRU {
	return &dedupLRU{max: max, seen: map[string]bool{}}
}

// Add returns true if key was NOT seen before (and records it).
func (d *dedupLRU) Add(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[key] {
		return false
	}
	d.seen[key] = true
	d.order = append(d.order, key)
	if len(d.order) > d.max {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	return true
}

func splitMessage(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var out []string
	for len(text) > limit {
		cut := strings.LastIndexByte(text[:limit], '\n')
		if cut < limit/2 {
			cut = limit
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
