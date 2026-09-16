// Package telegram bridges Spool loops to Telegram: one bot per loop, all in
// a shared group. Bots cannot see other bots' messages (platform rule), so
// loop-to-loop delivery is always internal; Telegram is a mirror plus the
// human I/O surface.
package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

const (
	pollTimeoutSec = 50
	maxMsgLen      = 4096
	sendSpacing    = time.Second // per-bot pacing (Telegram: ~1 msg/s)
	dedupSize      = 512
	// bindSettle is how long after binding a bot waits before it may win a
	// group's ingest election (ADR-0020): margin for clock skew between
	// Telegram's message dates and ours.
	bindSettle = 5 * time.Second
)

type Bridge struct {
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
}

// NewBridge builds the bridge. apiBase is the Bot API to talk to; "" means
// the live one.
func NewBridge(st store.Store, b *bus.Bus, r *route.Router, log *slog.Logger, apiBase string) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{store: st, bus: b, router: r, log: log, apiBase: apiBase,
		pollers: map[string]*poller{}, dedup: newDedupLRU(dedupSize),
		pairNotified: map[int64]bool{}}
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

// --- httpapi.Telegram interface ---

func (br *Bridge) ValidateToken(ctx context.Context, token string) (string, error) {
	u, err := NewClientAt(br.apiBase, token).GetMe(ctx)
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

func (br *Bridge) LoopChanged(l *store.Loop) {
	br.stopPoller(l.ID)
	if l.TGBotToken != "" && l.Status != store.StatusArchived {
		br.startPoller(l)
	}
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
}

func (br *Bridge) startPoller(l *store.Loop) {
	ctx, cancel := context.WithCancel(br.ctx)
	p := &poller{
		loopID: l.ID,
		name:   l.Name,
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
		})
		if err != nil && !errors.Is(err, store.ErrDuplicate) {
			br.log.Error("telegram group ingest", "err", err)
		}
		return
	}

	if m.Chat.Type == "private" {
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
		if !boundBefore(l, msgDate) {
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
// ingest a message Telegram dated at msgDate. bindSettle covers the skew
// between Telegram's clock and ours: erring long only delays a newcomer's
// first ingest by a few seconds, while erring short would let it duplicate
// what the incumbent already took. A binding from before this rule existed
// is recorded as 0 and always qualifies.
func boundBefore(l *store.Loop, msgDate int64) bool {
	if l.TGGroupBoundAt == 0 {
		return true
	}
	return msgDate > l.TGGroupBoundAt/1000+int64(bindSettle.Seconds())
}

// dedupKey identifies a telegram message: the chat, the id, and the bot that
// numbered it. It guards a poller re-reading its own updates; cross-bot
// duplicates are prevented upstream, by only one bot ingesting a group.
func dedupKey(loopID string, chatID, messageID int64) string {
	return fmt.Sprintf("%s:%d:%d", loopID, chatID, messageID)
}

// tgMsgAlias keeps handleMessage readable without exporting internals.
type tgMsgAlias = Message

// senderAllowed enforces the allowlist. Unknown senders are registered as
// pending with a pairing code; on DM they are told the code once per run.
func (br *Bridge) senderAllowed(ctx context.Context, p *poller, m *tgMsgAlias, author string, isGroup bool) bool {
	sender, err := br.store.TGSenders().Get(ctx, m.From.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		br.log.Error("sender lookup", "err", err)
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
				return false
			}
			// another poller registered them first; re-read
			if sender, err = br.store.TGSenders().Get(ctx, m.From.ID); err != nil {
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
		return false
	}
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
	l.TGGroupChatID = chatID
	l.TGGroupBoundAt = time.Now().UnixMilli()
	l.UpdatedAt = l.TGGroupBoundAt
	if err := br.store.Loops().Update(ctx, l); err != nil {
		br.log.Error("group bind", "err", err)
		return
	}
	br.log.Info("telegram group bound", "loop", l.Name, "chat_id", chatID)
	br.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: l.ID, Payload: map[string]any{
		"loop_id": l.ID, "name": l.Name, "tg_group_bound": true,
	}})
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
	for _, chunk := range splitMessage(text, maxMsgLen) {
		select {
		case p.sendCh <- sendReq{chatID: chatID, text: chunk}:
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
			for attempt := 0; attempt < 3; attempt++ {
				err := p.client.SendMessage(ctx, req.chatID, req.text)
				if err == nil {
					break
				}
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.Code == 429 {
					sleepCtx(ctx, time.Duration(max(apiErr.RetryAfter, 1))*time.Second)
					continue
				}
				br.log.Warn("telegram send failed", "loop", p.name, "err", err)
				break
			}
			sleepCtx(ctx, sendSpacing)
		}
	}
}

// --- mirroring ---

// mirror consumes message bus items and applies the mirror rules.
func (br *Bridge) mirror(ctx context.Context) {
	items, cancel := br.bus.Subscribe(func(i bus.Item) bool { return i.Kind == bus.KindMessage })
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
	switch mp.Origin {
	case store.OriginLoop:
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
			p.enqueueSend(l.TGGroupChatID, mp.Text)
		case store.ConversationOwnerDM:
			// a loop's owner_dm send: deliver to the captured DM chat as its
			// own bot. route.Send refuses when no capture exists, and a
			// capture implies the loop had a bot — so a miss on either here
			// is an internal fault, not a model error, and must not drop
			// the private message silently.
			if p == nil {
				br.log.Error("owner dm delivery: loop has no bot", "loop", mp.FromLoopID)
				return
			}
			chat, err := br.store.Messages().OwnerDMChat(ctx, mp.ConversationLoopID)
			if err != nil {
				br.log.Error("owner dm delivery: no captured chat", "loop", mp.FromLoopID, "err", err)
				return
			}
			p.enqueueSend(chat, mp.Text)
		}
		// control_room lives in the web UI alone; telegram sees nothing
	case store.OriginWeb:
		if mp.Conversation != store.ConversationGroup {
			// a per-loop composer message is the loop's private
			// control_room thread; only the group-wide web broadcast
			// is mirrored, until #80's web phase removes it
			return
		}
		// mirror web-origin group messages so Telegram lurkers see the
		// whole conversation; use the first delivered loop's bot that has
		// a bound group
		for _, loopID := range mp.DeliveredTo {
			p := br.poller(loopID)
			if p == nil {
				continue
			}
			l, err := br.store.Loops().Get(ctx, loopID)
			if err != nil || l.TGGroupChatID == 0 {
				continue
			}
			p.enqueueSend(l.TGGroupChatID, fmt.Sprintf("%s (via web): %s", mp.Author, mp.Text))
			return
		}
	}
	// telegram-origin messages are already visible in telegram: no re-mirror
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
