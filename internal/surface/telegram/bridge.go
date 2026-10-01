// Package telegram bridges Spool loops to Telegram: one bot per loop, all in
// a shared group. Bots cannot see other bots' messages (platform rule), so
// loop-to-loop delivery is always internal; Telegram is a mirror plus the
// human I/O surface.
package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/surface/outbound"
)

const (
	pollTimeoutSec = 50
	maxMsgLen      = 4096
	// maxCaptionLen is the most text a photo or document carries as its
	// caption. Longer words go ahead of the file as a message of their own.
	maxCaptionLen = 1024
	sendSpacing   = time.Second // per-bot pacing (Telegram: ~1 msg/s)
	dedupSize     = 512
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
	ledger  *outbound.Ledger

	// ctx is Start's, which Stop cancels. Every goroutine the bridge runs
	// is counted in running, and Stop waits for them.
	ctx     context.Context
	cancel  context.CancelFunc
	running sync.WaitGroup

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

	// loginTold holds, for each owner told that a Claude login was refused,
	// the loop whose bot told them, so the owner hears once per outage and
	// the all-clear arrives in the same chat. Only the mirror goroutine
	// touches it.
	loginTold map[loginOutage]loginTeller
}

// loginOutage is one refused login as one owner experiences it. The host's
// login and the Settings setup-token fail independently, so an owner with
// loops on both hears about each.
type loginOutage struct {
	owner     int64
	hostLogin bool
}

// loginTeller is the loop whose bot told the owner about an outage, and the
// chat it told them in.
type loginTeller struct {
	loopID string
	chatID int64
}

// NewBridge builds the bridge. apiBase is the Bot API to talk to; "" means
// the live one.
func NewBridge(st store.Store, publisher *bus.Bus, router *route.Router, log *slog.Logger, apiBase string) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{store: st, bus: publisher, router: router, log: log, apiBase: apiBase,
		ledger:     &outbound.Ledger{Store: st, Bus: publisher, Log: log, Surface: "telegram"},
		bindSettle: defaultBindSettle,
		pollers:    map[string]*poller{}, dedup: newDedupLRU(dedupSize),
		pairNotified: map[int64]bool{}, notOwnerNotified: map[string]bool{},
		loginTold: map[loginOutage]loginTeller{}}
}

// SetBindSettle overrides the ingest-election margin, and must be called
// before Start: the pollers read it as they run. Only a test harness pointed
// at a stand-in API should call it at all — against the real Telegram the
// margin is what keeps a newly bound bot from duplicating what the incumbent
// already ingested. A value of 0 or less keeps the default.
func (br *Bridge) SetBindSettle(settle time.Duration) {
	if settle > 0 {
		br.bindSettle = settle
	}
}

// Start launches pollers for every configured loop and the mirror consumer.
func (br *Bridge) Start(ctx context.Context) {
	br.mu.Lock()
	br.ctx, br.cancel = context.WithCancel(ctx)
	ctx = br.ctx
	br.mu.Unlock()
	loops, err := br.store.Loops().List(ctx)
	if err != nil {
		br.log.Error("telegram: list loops", "err", err)
		return
	}
	for _, loopRecord := range loops {
		if loopRecord.TGBotToken != "" && loopRecord.Status != store.StatusArchived {
			br.startPoller(loopRecord)
		}
	}
	br.running.Go(func() { br.mirror(ctx) })
}

// Stop cancels the mirror and every poller, and waits for them: each send
// loop fails what its bot still held on the way out.
func (br *Bridge) Stop(ctx context.Context) {
	br.mu.Lock()
	cancel := br.cancel
	if cancel != nil {
		cancel()
	}
	br.mu.Unlock()
	if cancel != nil {
		surface.Wait(ctx, &br.running)
	}
}

// --- surface.Surface interface ---

// ValidateCredential resolves a bot token to its bot username. Telegram has
// one token per bot, so a refusal is always of the token.
func (br *Bridge) ValidateCredential(ctx context.Context, credential surface.Credential) (surface.Identity, error) {
	user, err := NewClientAt(br.apiBase, credential.Token).GetMe(ctx)
	if err != nil {
		var refused *APIError
		if errors.As(err, &refused) {
			return surface.Identity{}, &surface.RejectedError{Part: surface.PartToken, Err: err}
		}
		return surface.Identity{}, err
	}
	return surface.Identity{Name: user.Username}, nil
}

// LoopChanged brings the loop's poller in line with its stored configuration.
// The row is read here rather than handed in: the hub says only that a loop
// changed, and which of its fields matter is the bridge's own business — so
// this is called for every edit, and answering "nothing to do" is part of the
// job. It has to be: a restart costs a replay. getUpdates offsets are held by
// the poller, so a new one starts at 0 and Telegram re-sends everything it
// still holds, for the message key and the dedup LRU to throw away again.
func (br *Bridge) LoopChanged(ctx context.Context, loopID string) {
	loopRecord, err := br.store.Loops().Get(ctx, loopID)
	if err != nil {
		br.log.Error("telegram: read changed loop", "loop", loopID, "err", err)
		return
	}
	if loopRecord.TGBotToken == "" || loopRecord.Status == store.StatusArchived {
		br.stopPoller(loopID)
		return
	}
	if bot := br.poller(loopID); bot != nil && bot.token == loopRecord.TGBotToken {
		return // same bot, still polling: the edit was none of our business
	}
	br.stopPoller(loopID)
	br.startPoller(loopRecord)
}

func (br *Bridge) LoopRemoved(loopID string) { br.stopPoller(loopID) }

func (br *Bridge) Status(loopID string) any {
	br.mu.Lock()
	defer br.mu.Unlock()
	bot, ok := br.pollers[loopID]
	if !ok {
		return map[string]any{"polling": false}
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	return map[string]any{
		"polling":        true,
		"last_update_at": bot.lastUpdate,
		"last_error":     bot.lastError,
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
	// stopped is set, under sendMu, once the send loop has quit and taken
	// what sendCh held: a send queued after that would sit there unsent
	// and unrecorded.
	sendMu  sync.Mutex
	stopped bool

	mu         sync.Mutex
	lastUpdate int64
	lastError  string
}

// sendReq is one message to send: a long one is split when it is sent, and
// lands or fails whole.
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
	// media is the file the message carries (#123), nil for words alone.
	media *Media
}

func (br *Bridge) startPoller(loopRecord *store.Loop) {
	br.mu.Lock()
	defer br.mu.Unlock()
	// A stopped bridge starts nothing: Stop has cancelled, under this lock,
	// and may already be waiting for the last poller to finish.
	if br.ctx == nil || br.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(br.ctx)
	bot := &poller{
		loopID: loopRecord.ID,
		name:   loopRecord.Name,
		token:  loopRecord.TGBotToken,
		client: NewClientAt(br.apiBase, loopRecord.TGBotToken),
		cancel: cancel,
		sendCh: make(chan sendReq, 128),
	}
	br.pollers[loopRecord.ID] = bot
	br.running.Go(func() { br.pollLoop(ctx, bot) })
	br.running.Go(func() { br.sendLoop(ctx, bot) })
	br.log.Info("telegram poller started", "loop", loopRecord.Name, "bot", loopRecord.TGBotUsername)
}

func (br *Bridge) stopPoller(loopID string) {
	br.mu.Lock()
	bot, ok := br.pollers[loopID]
	if ok {
		delete(br.pollers, loopID)
	}
	br.mu.Unlock()
	if ok {
		bot.cancel()
	}
}

func (br *Bridge) pollLoop(ctx context.Context, bot *poller) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := bot.client.GetUpdates(ctx, offset, pollTimeoutSec)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			bot.mu.Lock()
			bot.lastError = err.Error()
			bot.mu.Unlock()
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				switch apiErr.Code {
				case 401:
					br.log.Error("telegram token invalid; poller stopped", "loop", bot.name)
					return
				case 409:
					br.log.Error("telegram 409: another getUpdates consumer for this bot; poller paused 60s", "loop", bot.name)
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
		bot.mu.Lock()
		bot.lastError = ""
		bot.lastUpdate = time.Now().UnixMilli()
		bot.mu.Unlock()
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.Message != nil {
				br.handleMessage(ctx, bot, update.Message)
			}
		}
	}
}

func (br *Bridge) handleMessage(ctx context.Context, bot *poller, message *tgMsgAlias) {
	if message.From == nil || message.From.IsBot {
		return // defensive: Telegram shouldn't deliver bot messages at all
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}
	attachments := attachmentsOf(bot, message)
	if text == "" && len(attachments) == 0 {
		return
	}
	author := "someone"
	if message.From.Username != "" {
		author = message.From.Username
	} else if message.From.FirstName != "" {
		author = message.From.FirstName
	}

	isGroup := message.Chat.Type == "group" || message.Chat.Type == "supergroup"

	// Access gate: only allowlisted senders reach loops or affect state.
	// Unknown senders get a pending record + pairing code; strangers can't
	// bind groups, trigger /spool_status, or message loops.
	if !br.senderAllowed(ctx, bot, message, author, isGroup) {
		return
	}

	if isGroup {
		br.maybeBindGroup(ctx, bot, message.Chat.ID)
	}

	// /spool_status works even with bot privacy mode on and forces binding
	if strings.HasPrefix(text, "/spool_status") {
		br.replyStatus(ctx, bot, message.Chat.ID)
		return
	}

	// Every bot that saw the message records its own id for it, whether or
	// not it is the one that ingests it. That sighting is what later lets
	// this bot's reply thread under the message: it cannot borrow the
	// ingesting bot's id, which belongs to another numbering (ADR-0020).
	// It also carries what the message replies to, when this bot can tell
	// by its own id for the target (#424).
	sightedTarget := br.ownReplyTarget(ctx, bot, message)
	br.recordSighting(ctx, bot, message, sightedTarget)

	if isGroup {
		br.ingestGroupMessage(ctx, bot, message, author, text, attachments, sightedTarget)
		return
	}

	if message.Chat.Type == "private" {
		br.maybeCaptureOwnerDM(ctx, bot, message)
		if !br.ownerOf(ctx, bot, message.From.ID) {
			br.logTurnedAway(bot, message, author, "sender is not this loop's owner")
			// Not the loop's owner. Delivering this would leave the loop
			// unable to answer — owner_dm addresses the owner, so the reply
			// would land in someone else's chat. Until non-owner private
			// conversations are designed, say so instead of going silent
			// (ADR-0026 amendment).
			br.notifyNotOwner(ctx, bot, message.Chat.ID)
			return
		}
		if !br.dedup.Add(dedupKey(bot.loopID, message.Chat.ID, message.MessageID)) {
			return
		}
		err := br.router.Ingest(ctx, route.InboundMessage{
			Origin:      store.OriginTelegramDM,
			Author:      author,
			Text:        text,
			TGChatID:    message.Chat.ID,
			TGMessageID: message.MessageID,
			TGBotLoopID: bot.loopID,
			TGKey:       tgKey(message),
			ReplyToID:   br.inboundReplyTarget(ctx, bot, message),
			ImplicitTo:  bot.loopID,
			Attachments: attachments,
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
	for _, loopRecord := range loops {
		if loopRecord.TGGroupChatID != chatID || loopRecord.Status == store.StatusArchived {
			continue
		}
		if !boundBefore(loopRecord, msgDate, br.bindSettle) {
			continue
		}
		if _, polling := br.pollers[loopRecord.ID]; !polling {
			continue
		}
		if ingest == "" || loopRecord.ID < ingest {
			ingest = loopRecord.ID
		}
	}
	return ingest
}

// boundBefore reports whether loopRecord's bot was bound to its group early
// enough to ingest a message Telegram dated at msgDate. The settle margin
// covers the skew between Telegram's clock and ours: erring long only delays a
// newcomer's first ingest by a few seconds, while erring short would let it
// duplicate what the incumbent already took. A binding from before this rule
// existed is recorded as 0 and always qualifies.
func boundBefore(loopRecord *store.Loop, msgDate int64, settle time.Duration) bool {
	if loopRecord.TGGroupBoundAt == 0 {
		return true
	}
	return msgDate > loopRecord.TGGroupBoundAt/1000+int64(settle.Seconds())
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
func tgKey(message *tgMsgAlias) string {
	// A photo's words are its caption; a text message has none, so its key
	// is what it always was.
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%d|%d|%s", message.Chat.ID, message.From.ID, message.Date, message.Text+message.Caption))
	return hex.EncodeToString(sum[:16])
}

// attachmentsOf is the file a message carries, as the router takes it:
// fetched through the bot that saw it, and only if its message is stored
// (#123). A photo is the largest size Telegram made of it.
func attachmentsOf(bot *poller, message *tgMsgAlias) []route.InboundAttachment {
	fetch := func(fileID string) func(context.Context) (io.ReadCloser, error) {
		return func(ctx context.Context) (io.ReadCloser, error) { return bot.client.Download(ctx, fileID) }
	}
	switch {
	case len(message.Photo) > 0:
		largest := message.Photo[len(message.Photo)-1]
		return []route.InboundAttachment{{
			// Telegram gives a photo no name; its message id makes one
			Name:  fmt.Sprintf("photo-%d.jpg", message.MessageID),
			Kind:  store.AttachmentImage,
			Size:  largest.FileSize,
			Fetch: fetch(largest.FileID),
		}}
	case message.Document != nil:
		name := message.Document.FileName
		if name == "" {
			name = fmt.Sprintf("document-%d", message.MessageID)
		}
		return []route.InboundAttachment{{
			Name:  name,
			Size:  message.Document.FileSize,
			Fetch: fetch(message.Document.FileID),
		}}
	}
	return nil
}

// recordSighting stores this bot's own id for a message it received —
// every inbound message, group or DM, since a DM's only reference is the
// receiving bot's sighting of it — and what this bot resolved it to reply
// to, 0 for nothing.
func (br *Bridge) recordSighting(ctx context.Context, bot *poller, message *tgMsgAlias, replyToID int64) {
	err := br.store.Messages().RecordSighting(ctx, tgKey(message), bot.loopID, message.Chat.ID, message.MessageID, replyToID, time.Now().UnixMilli())
	if err != nil {
		br.log.Warn("telegram: record sighting", "loop", bot.name, "err", err)
	}
}

// recordSentRef maps an internal message to the id Telegram minted for it in
// this bot's numbering, so a later reply can target it.
func (br *Bridge) recordSentRef(ctx context.Context, bot *poller, req sendReq, sent *Message) {
	if req.recordFor == 0 || sent == nil || sent.MessageID == 0 {
		return
	}
	err := br.store.Messages().PutRef(ctx, &store.SurfaceRef{
		MessageID: req.recordFor, BotLoopID: bot.loopID,
		TGChatID: req.chatID, TGMessageID: sent.MessageID,
	})
	if err != nil {
		br.log.Warn("telegram: record sent reference", "loop", bot.name, "err", err)
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
func (br *Bridge) inboundReplyTarget(ctx context.Context, bot *poller, message *tgMsgAlias) int64 {
	rm := message.ReplyToMessage
	if rm == nil || rm.From == nil {
		return 0
	}
	msgs := br.store.Messages()
	if target, err := msgs.ByRef(ctx, bot.loopID, message.Chat.ID, rm.MessageID); err == nil {
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

// ingestGroupMessage persists a group message if this bot is the one elected
// to, and otherwise lends it the reply target only this bot could see.
func (br *Bridge) ingestGroupMessage(ctx context.Context, bot *poller, message *tgMsgAlias, author, text string, attachments []route.InboundAttachment, sightedTarget int64) {
	// Every bot in the group sees this message under its own message_id,
	// so exactly one of them may persist it.
	if br.groupIngestLoopID(ctx, message.Chat.ID, message.Date) != bot.loopID {
		// In a basic group only the author's bot sees what a reply to
		// its post answers, and it is seldom the one that ingests. It
		// hands the ingested row the target, if the row is already
		// there; if not, the ingesting bot finds it in the sighting.
		if sightedTarget != 0 {
			br.adoptReplyTarget(ctx, bot, tgKey(message), sightedTarget)
		}
		return
	}
	if !br.dedup.Add(dedupKey(bot.loopID, message.Chat.ID, message.MessageID)) {
		return
	}
	replyTo := br.inboundReplyTarget(ctx, bot, message)
	if replyTo == 0 {
		if sighted, err := br.store.Messages().SightedReplyTarget(ctx, tgKey(message)); err == nil {
			replyTo = sighted
		}
	}
	err := br.router.Ingest(ctx, route.InboundMessage{
		Origin:      store.OriginTelegramGroup,
		Author:      author,
		Text:        text,
		TGChatID:    message.Chat.ID,
		TGMessageID: message.MessageID,
		TGBotLoopID: bot.loopID,
		TGKey:       tgKey(message),
		ReplyToID:   replyTo,
		Attachments: attachments,
	})
	if err != nil && !errors.Is(err, store.ErrDuplicate) {
		br.log.Error("telegram group ingest", "err", err)
	}
	if err == nil && replyTo == 0 {
		// The author's bot may have recorded the target between the
		// look above and the insert, and looked for the row before it
		// existed. Looking again after the insert closes that gap: of
		// the two, whichever looks last sees the other's write.
		if sighted, err := br.store.Messages().SightedReplyTarget(ctx, tgKey(message)); err == nil {
			br.adoptReplyTarget(ctx, bot, tgKey(message), sighted)
		} else {
			br.log.Debug("telegram: group message replies to nothing this hub can name",
				"loop", bot.name, "embedded", message.ReplyToMessage != nil)
		}
	}
}

// ownReplyTarget is the message a reply answers, when this bot holds its own
// id for it: a message it sent or saw. That is certain where every other
// resolution is a match, and in a basic group it is the only one that works
// for a reply to a loop's post, because only the author's bot finds the
// post embedded in the reply (#424).
func (br *Bridge) ownReplyTarget(ctx context.Context, bot *poller, message *tgMsgAlias) int64 {
	rm := message.ReplyToMessage
	if rm == nil || rm.MessageID == 0 {
		return 0
	}
	target, err := br.store.Messages().ByRef(ctx, bot.loopID, message.Chat.ID, rm.MessageID)
	if err != nil {
		return 0
	}
	return target.ID
}

// adoptReplyTarget gives an ingested group message the reply target it was
// ingested without, and delivers it to that target's author. Two bots may
// both try; the store lets one of them.
func (br *Bridge) adoptReplyTarget(ctx context.Context, bot *poller, key string, targetID int64) {
	adopted, err := br.store.Messages().AdoptReplyTarget(ctx, key, targetID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		br.log.Error("telegram: adopt reply target", "loop", bot.name, "err", err)
		return
	}
	if err := br.router.DeliverAdoptedReply(ctx, adopted); err != nil {
		br.log.Error("telegram: deliver adopted reply", "loop", bot.name, "message", adopted.ID, "err", err)
	}
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
	for _, loopRecord := range loops {
		if strings.EqualFold(strings.TrimPrefix(loopRecord.TGBotUsername, "@"), username) {
			return loopRecord.ID
		}
	}
	return ""
}

// withoutQuotePrefix removes the line render prepends when it cannot anchor
// a reply natively, returning what the store holds. Text that carries no
// such line comes back unchanged, so the caller can try both.
func withoutQuotePrefix(text string) string {
	if !strings.HasPrefix(text, outbound.QuoteMark) {
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
	return 0, outbound.QuotePrefix(target) + mp.Text
}

// tgMsgAlias keeps handleMessage readable without exporting internals.
type tgMsgAlias = Message

// senderAllowed enforces the allowlist. Unknown senders are registered as
// pending with a pairing code; on DM they are told the code once per run.
func (br *Bridge) senderAllowed(ctx context.Context, bot *poller, message *tgMsgAlias, author string, isGroup bool) bool {
	sender, err := br.store.TGSenders().Get(ctx, message.From.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		br.log.Error("sender lookup", "err", err)
		br.logTurnedAway(bot, message, author, "sender lookup failed")
		return false // fail closed
	}

	if errors.Is(err, store.ErrNotFound) {
		via := "dm:" + bot.name
		if isGroup {
			via = "group:" + bot.name
		}
		now := time.Now().UnixMilli()
		sender = &store.TGSender{
			TGUserID:     message.From.ID,
			Username:     message.From.Username,
			Display:      strings.TrimSpace(message.From.FirstName),
			Status:       store.SenderPending,
			PairCode:     surface.PairCode(),
			FirstSeenVia: via,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if cerr := br.store.TGSenders().Create(ctx, sender); cerr != nil {
			if !errors.Is(cerr, store.ErrDuplicate) {
				br.log.Error("sender create", "err", cerr)
				br.logTurnedAway(bot, message, author, "sender could not be registered")
				return false
			}
			// another poller registered them first; re-read
			if sender, err = br.store.TGSenders().Get(ctx, message.From.ID); err != nil {
				br.logTurnedAway(bot, message, author, "sender registered by another poller but unreadable")
				return false
			}
		} else {
			br.log.Info("new telegram sender pending approval", "user", author, "id", message.From.ID)
			br.bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender.Frame()})
		}
	}

	switch sender.Status {
	case store.SenderAllowed:
		return true
	case store.SenderBlocked:
		br.logTurnedAway(bot, message, author, "sender is blocked")
		return false
	default: // pending
		if !isGroup {
			br.pairMu.Lock()
			notified := br.pairNotified[message.From.ID]
			br.pairNotified[message.From.ID] = true
			br.pairMu.Unlock()
			if !notified {
				bot.enqueueSend(message.Chat.ID, fmt.Sprintf(
					"Spool: you're not authorized yet. Your pairing code is %s — ask the operator to approve you in the Spool control room (Access page).",
					sender.PairCode))
			}
		}
		br.logTurnedAway(bot, message, author, "sender is pending approval")
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
func (br *Bridge) logTurnedAway(bot *poller, message *tgMsgAlias, author, reason string) {
	br.log.Info("telegram inbound discarded", "loop", bot.name, "reason", reason,
		"author", author, "chat_type", message.Chat.Type, "chat_id", message.Chat.ID,
		"message_id", message.MessageID)
}

func (br *Bridge) maybeBindGroup(ctx context.Context, bot *poller, chatID int64) {
	loopRecord, err := br.store.Loops().Get(ctx, bot.loopID)
	if err != nil || loopRecord.TGGroupChatID == chatID {
		return
	}
	boundAt := time.Now().UnixMilli()
	if _, err := br.store.Rooms().Bind(ctx, loopRecord.ID, store.SurfaceTelegram, strconv.FormatInt(chatID, 10),
		store.FleetChannel, boundAt); err != nil {
		br.log.Error("group bind", "err", err)
		return
	}
	br.log.Info("telegram group bound", "loop", loopRecord.Name, "chat_id", chatID)
	br.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
		"loop_id": loopRecord.ID, "name": loopRecord.Name, "tg_group_bound": true,
	}})
}

// maybeCaptureOwnerDM records where this loop's bot can write privately to
// its configured owner. A bot cannot open a private chat, so the address
// only exists once the owner has written to this bot at least once — and it
// is that bot's chat, not another's (#73). Only the configured owner's chat
// is captured: being allowlisted, or messaging first, does not make someone
// the owner.
func (br *Bridge) maybeCaptureOwnerDM(ctx context.Context, bot *poller, message *tgMsgAlias) {
	loopRecord, err := br.store.Loops().Get(ctx, bot.loopID)
	if err != nil || loopRecord.OwnerTGUserID == 0 || loopRecord.OwnerTGUserID != message.From.ID {
		return
	}
	if loopRecord.OwnerDMChatID == message.Chat.ID {
		return
	}
	if err := br.store.Loops().SetOwnerDMChat(ctx, loopRecord.ID, message.Chat.ID, time.Now().UnixMilli()); err != nil {
		br.log.Error("owner dm capture", "loop", loopRecord.Name, "err", err)
		return
	}
	br.log.Info("owner dm captured", "loop", loopRecord.Name, "chat_id", message.Chat.ID)
	br.bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: loopRecord.ID, Payload: map[string]any{
		"loop_id": loopRecord.ID, "name": loopRecord.Name, "owner_dm_ready": true,
	}})
}

// ownerOf reports whether tgUserID is the loop's configured owner.
func (br *Bridge) ownerOf(ctx context.Context, bot *poller, tgUserID int64) bool {
	loopRecord, err := br.store.Loops().Get(ctx, bot.loopID)
	return err == nil && loopRecord.OwnerTGUserID != 0 && loopRecord.OwnerTGUserID == tgUserID
}

// notifyNotOwner tells a non-owner, once per loop per run, why their DM
// goes unanswered. The same pacing as the pairing code — a turned away
// sender should learn the reason, not be answered on every message — but
// per loop, because each one answers a different owner.
func (br *Bridge) notifyNotOwner(ctx context.Context, bot *poller, chatID int64) {
	key := fmt.Sprintf("%s:%d", bot.loopID, chatID)
	br.pairMu.Lock()
	notified := br.notOwnerNotified[key]
	br.notOwnerNotified[key] = true
	br.pairMu.Unlock()
	if notified {
		return
	}
	owner := "its owner"
	if loopRecord, err := br.store.Loops().Get(ctx, bot.loopID); err == nil && loopRecord.OwnerTGUserID != 0 {
		if sender, err := br.store.TGSenders().Get(ctx, loopRecord.OwnerTGUserID); err == nil && sender.Username != "" {
			owner = "@" + sender.Username
		}
	}
	bot.enqueueSend(chatID, fmt.Sprintf(
		"Spool: %s reads direct messages only from %s for now. Reach it in the group instead.",
		bot.name, owner))
}

func (br *Bridge) replyStatus(ctx context.Context, bot *poller, chatID int64) {
	loopRecord, err := br.store.Loops().Get(ctx, bot.loopID)
	if err != nil {
		return
	}
	next := "none scheduled"
	if entry, err := br.store.Schedule().Get(ctx, loopRecord.ID); err == nil && entry.NextTickAt > 0 {
		next = time.UnixMilli(entry.NextTickAt).UTC().Format("15:04 UTC")
	}
	bot.enqueueSend(chatID, fmt.Sprintf("spool: loop %q is %s · next tick %s", loopRecord.Name, loopRecord.Status, next))
}

// --- outbound: per-bot paced sender ---

// enqueueSend queues a bridge notice, which no message row records: a full
// queue drops it, as it would any notice.
func (bot *poller) enqueueSend(chatID int64, text string) {
	bot.enqueue(sendReq{chatID: chatID, text: text})
}

// enqueue queues a send whole, and returns "" when it did and otherwise why
// not: the queue is full, or the bot has stopped. The caller records the
// loss, since the send loop that would have recorded it never sees the send.
func (bot *poller) enqueue(req sendReq) string {
	bot.sendMu.Lock()
	defer bot.sendMu.Unlock()
	if bot.stopped {
		return errBotStopped
	}
	select {
	case bot.sendCh <- req:
		return ""
	default: // queue full: drop rather than block the bridge
		return outbound.ErrQueueFull
	}
}

// errBotStopped is the failure of a send a loop's bot stopped before
// sending: it was replaced, removed, or its loop archived.
const errBotStopped = "the loop's Telegram bot stopped before this was sent"

func (br *Bridge) sendLoop(ctx context.Context, bot *poller) {
	for {
		select {
		case <-ctx.Done():
			br.abandonQueue(ctx, bot)
			return
		case req := <-bot.sendCh:
			br.deliver(ctx, bot, req)
			sleepCtx(ctx, sendSpacing)
		}
	}
}

// abandonQueue fails what a stopped bot still had queued, and refuses what
// comes after: the loop believes it spoke, and a send left in the queue of
// a bot that is gone would be lost without a record (#302).
func (br *Bridge) abandonQueue(ctx context.Context, bot *poller) {
	bot.sendMu.Lock()
	bot.stopped = true
	bot.sendMu.Unlock()
	for {
		select {
		case req := <-bot.sendCh:
			br.unsent(ctx, bot, req, errors.New(br.stopReason()))
		default:
			return
		}
	}
}

// unsent records a send its bot stopped before finishing, as failed with
// err. A notice is only dropped: no row records it, and the timeline has no
// gap to show.
func (br *Bridge) unsent(ctx context.Context, bot *poller, req sendReq, err error) {
	if req.recordFor == 0 {
		return
	}
	br.failSend(settleCtx(ctx), bot, req, err, 0)
}

// stopReason is why a send its bot held never went: the hub stopping, or
// the bot alone.
func (br *Bridge) stopReason() string {
	if br.ctx != nil && br.ctx.Err() != nil {
		return outbound.ErrUnsentAtStop
	}
	return errBotStopped
}

// settleCtx is the context a send's outcome is written through: its bot's
// own, until the bot stops. A send that ends as its bot stops, whether it
// was cut short or its last part landed, must still be recorded, so a
// spent context is traded for one without the cancel. The hub closes the
// store only once Stop has waited for that write.
func settleCtx(ctx context.Context) context.Context {
	if ctx.Err() == nil {
		return ctx
	}
	return context.WithoutCancel(ctx)
}

// deliver sends a message in Telegram-sized parts, then the file it
// carries, and records how it ended. Only the first part carries the reply anchor and mints the
// message's surface reference: the rest are the same message, not new
// targets. A part that does not land fails the whole message, since the
// loop's words did not all arrive, even when its start did.
func (br *Bridge) deliver(ctx context.Context, bot *poller, req sendReq) {
	parts := sendParts(bot.client, req)
	for i, send := range parts {
		if i > 0 && !sleepCtx(ctx, sendSpacing) {
			br.unsent(ctx, bot, req, partOf(errors.New(br.stopReason()), i, len(parts)))
			return
		}
		sent, attempts, err := br.sendWithRetries(ctx, bot, send)
		if err != nil {
			if ctx.Err() != nil {
				// The bot stopped under the attempt: that, not Telegram's
				// answer, is why this part never arrived.
				br.unsent(ctx, bot, req, partOf(errors.New(br.stopReason()), i, len(parts)))
				return
			}
			br.failSend(ctx, bot, req, partOf(err, i, len(parts)), attempts)
			return
		}
		if i == 0 {
			br.recordSentRef(settleCtx(ctx), bot, req, sent)
		}
	}
	br.ledger.Result(settleCtx(ctx), req.recordFor, nil)
}

// sendPart is one Bot API call a message is sent in.
type sendPart func(ctx context.Context) (*Message, error)

// sendParts splits a message into the calls that send it: its words in
// Telegram-sized parts, the first replying to req.replyTo, then its file.
// Words short enough to caption the file go with it as one call instead.
func sendParts(client *Client, req sendReq) []sendPart {
	if req.media != nil && utf16Len(req.text) <= maxCaptionLen {
		return []sendPart{func(ctx context.Context) (*Message, error) {
			return client.SendMedia(ctx, req.chatID, *req.media, req.text, req.replyTo)
		}}
	}
	var parts []sendPart
	for i, text := range splitMessage(req.text, maxMsgLen) {
		replyTo := int64(0)
		if i == 0 {
			replyTo = req.replyTo
		}
		parts = append(parts, func(ctx context.Context) (*Message, error) {
			return client.SendMessage(ctx, req.chatID, text, replyTo)
		})
	}
	if req.media != nil {
		parts = append(parts, func(ctx context.Context) (*Message, error) {
			return client.SendMedia(ctx, req.chatID, *req.media, "", 0)
		})
	}
	return parts
}

// utf16Len is text's length as Telegram measures its limits: in UTF-16
// code units, so a character outside the Basic Multilingual Plane, as most
// emoji are, counts twice.
func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// partOf names the part of an n-part message err ended at, index i; a
// message sent whole needs no part named.
func partOf(err error, i, n int) error {
	if n == 1 {
		return err
	}
	return fmt.Errorf("part %d of %d: %w", i+1, n, err)
}

// sendWithRetries makes a send survive the ordinary failure — a timeout or a
// 5xx against api.telegram.org — instead of costing the message. A rejection
// Telegram means (a 4xx that is not 429) is not retried: sending it again
// would fail the same way, slower. It returns the error the last attempt
// ended on, and how many attempts it made; a cancelled context ends it
// early.
func (br *Bridge) sendWithRetries(ctx context.Context, bot *poller, send sendPart) (*Message, int, error) {
	var lastErr error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		sent, err := send(ctx)
		if err == nil {
			return sent, attempt + 1, nil
		}
		if ctx.Err() != nil {
			return nil, attempt + 1, ctx.Err()
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
				if !sleepCtx(ctx, time.Duration(max(apiErr.RetryAfter, 1))*time.Second) {
					return nil, attempt + 1, ctx.Err()
				}
			}
		case errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500:
			return nil, attempt + 1, err
		default:
			if attempt < sendAttempts-1 {
				br.log.Warn("telegram send failed; retrying",
					"loop", bot.name, "attempt", attempt+1, "err", err)
				if !sleepCtx(ctx, sendBackoff<<attempt) {
					return nil, attempt + 1, ctx.Err()
				}
			}
		}
	}
	return nil, sendAttempts, lastErr
}

// failSend gives up on a send and leaves the evidence in the two places
// somebody would look: the message row and the loop's timeline.
func (br *Bridge) failSend(ctx context.Context, bot *poller, req sendReq, err error, attempts int) {
	br.log.Error("telegram send failed; giving up",
		"loop", bot.name, "chat", req.chatID, "attempts", attempts, "err", err)
	br.ledger.Result(ctx, req.recordFor, err)
	br.ledger.FailedEvent(ctx, bot.loopID, br.chatName(ctx, bot, req.chatID), attempts, err.Error(), req.text)
}

// chatName says which conversation a send was aimed at, in the operator's
// terms rather than Telegram's. A chat id names nothing a reader knows; what
// they need from a lost message is who never heard it.
func (br *Bridge) chatName(ctx context.Context, bot *poller, chatID int64) string {
	loopRecord, err := br.store.Loops().Get(ctx, bot.loopID)
	if err != nil {
		// The operator reads "a chat" either way, but a store that cannot be
		// read during a send failure is a second problem, not a naming one.
		br.log.Warn("telegram: name chat for send failure", "loop", bot.name, "err", err)
		return "a chat"
	}
	switch chatID {
	case loopRecord.TGGroupChatID:
		return "the group"
	case loopRecord.OwnerDMChatID:
		return "the owner"
	default:
		return "a chat"
	}
}

// --- mirroring ---

// mirror consumes message bus items and applies the mirror rules, and
// delivers the hub's login notices to owners.
func (br *Bridge) mirror(ctx context.Context) {
	// Both kinds, one path: a retry (#269) is the same send of the same row,
	// asked for by the operator instead of by the loop, and anything the
	// mirror rules decide about a message must decide the same way twice.
	// Lossless: an item dropped here is a loop's send lost without a
	// record (#302).
	items, cancel := br.bus.SubscribeLossless(func(item bus.Item) bool {
		return item.Kind == bus.KindMessage || item.Kind == bus.KindSendRetry || item.Kind == bus.KindClaudeLogin
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
			switch payload := item.Payload.(type) {
			case *route.MessagePayload:
				br.mirrorMessage(ctx, payload)
			case *surface.LoginNotice:
				br.noticeLogin(ctx, payload)
			}
		}
	}
}

func (br *Bridge) mirrorMessage(ctx context.Context, mp *route.MessagePayload) {
	// Only a loop's words leave the hub. Telegram-origin messages are
	// already visible in telegram, and nothing the operator writes in the
	// control room is mirrored outward — not to the group, not anywhere
	// (ADR-0032). That is the operator's security posture rather
	// than a gap: Spool holds no means of posting his words on a third
	// party, so a bug here cannot become a message sent as him. The test
	// is on the origin, not the conversation, so a destination added later
	// inherits the rule instead of having to remember it.
	if mp.Origin != store.OriginLoop {
		return
	}
	loopRecord, err := br.store.Loops().Get(ctx, mp.FromLoopID)
	if err != nil {
		br.log.Warn("telegram: read loop for delivery", "loop", mp.FromLoopID, "err", err)
		if mp.Mirror == store.MirrorPending {
			br.ledger.Unsendable(ctx, mp, "delivery: read loop: "+err.Error())
		}
		return
	}
	// A loop on another surface is that surface's to carry: a loop has one
	// (ADR-0029). A loop on none is still this bridge's to settle, as it
	// always was: a send bound for a bot that has since gone stays on the
	// hub, or fails, below.
	if onSurface := loopRecord.Surface(); onSurface != "" && onSurface != store.SurfaceTelegram {
		return
	}
	bot := br.poller(mp.FromLoopID)
	// By destination, not kind: a channel other than the fleet channel has
	// no room on any surface yet, so its messages stay on the hub
	// (ADR-0038) and match no case here.
	switch mp.Destination() {
	case store.ConversationGroup:
		// a loop's explicit group send: post to its bound group as its
		// own bot, judged from the loop as it is now rather than as
		// route.Send saw it — a group bound since the send still gets
		// the post. No bot or no group means there is no room to carry
		// it, normal for a fleet without telegram, so the message stays
		// on the hub. A bound bot with no poller is the same internal
		// fault as the owner_dm case below.
		if loopRecord.TGBotToken == "" || loopRecord.TGGroupChatID == 0 {
			br.ledger.StayOnHub(ctx, mp)
			return
		}
		if bot == nil {
			br.ledger.Unsendable(ctx, mp, "group delivery: loop's bot is not running")
			return
		}
		media, ok := br.sentMedia(ctx, mp)
		if !ok {
			return
		}
		anchor, text := br.render(ctx, mp, loopRecord.TGGroupChatID)
		if unsent := bot.enqueue(sendReq{chatID: loopRecord.TGGroupChatID, text: text, replyTo: anchor, recordFor: mp.ID, media: media}); unsent != "" {
			br.ledger.Unsendable(ctx, mp, unsent)
		}
	case store.ConversationOwnerDM:
		// a loop's owner_dm send: deliver to the chat route.Send pinned
		// at send time — never re-resolved here, so a DM arriving
		// between send and delivery cannot redirect it. route.Send
		// refuses when no chat resolves, and a captured chat implies
		// the loop had a bot — so a miss on either here is an internal
		// fault, not a model error, and must not drop the private
		// message silently.
		if bot == nil {
			br.ledger.Unsendable(ctx, mp, "owner dm delivery: loop has no bot")
			return
		}
		if mp.OwnerDMChat == 0 {
			br.ledger.Unsendable(ctx, mp, "owner dm delivery: send carried no pinned chat")
			return
		}
		media, ok := br.sentMedia(ctx, mp)
		if !ok {
			return
		}
		anchor, text := br.render(ctx, mp, mp.OwnerDMChat)
		if unsent := bot.enqueue(sendReq{chatID: mp.OwnerDMChat, text: text, replyTo: anchor, recordFor: mp.ID, media: media}); unsent != "" {
			br.ledger.Unsendable(ctx, mp, unsent)
		}
	}
	// control_room lives in the web UI alone; telegram sees nothing
}

// sentMedia is the file a loop's message carries, nil for none. A file the
// hub no longer keeps, or cannot look up, fails the send rather than
// sending the words without it: the loop sent them together. ok is false
// when it has.
func (br *Bridge) sentMedia(ctx context.Context, mp *route.MessagePayload) (*Media, bool) {
	if br.router == nil {
		return nil, true // a bridge built for its delivery rules alone, in tests
	}
	row, hostPath, err := br.router.SentAttachment(ctx, mp.ID)
	if err != nil {
		br.ledger.Unsendable(ctx, mp, "attachment: "+err.Error())
		return nil, false
	}
	if row == nil {
		return nil, true
	}
	return &Media{Path: hostPath, Name: row.Name, Photo: asPhoto(row)}, true
}

// asPhoto reports whether Telegram takes a file as a photo: a JPEG or PNG
// of at most 10 MB, its sides summing to at most 10000 and neither more
// than 20 times the other. Anything else goes as a document, which only
// the 50 MB bot upload limit bounds, well above the hub's own.
func asPhoto(row *store.Attachment) bool {
	if row.MIME != "image/jpeg" && row.MIME != "image/png" {
		return false
	}
	width, height := row.Width, row.Height
	return row.Size <= 10<<20 && width > 0 && height > 0 &&
		width+height <= 10000 && width <= 20*height && height <= 20*width
}

// noticeLogin tells a loop's owner, in its bot's private chat with them, that
// the Claude login was refused, and later that it works again (#419). One
// login stops every loop that shares it, each at its own next turn, so only
// the first refusal an owner hears about is told; the rest of the outage is
// already known to them. A loop that cannot reach its owner privately leaves
// the telling to the next one that can, and its timeline says so.
//
// What was told is remembered for this run only. A hub restarted mid-outage
// tells the owner again, once, which is the better failure than silence.
func (br *Bridge) noticeLogin(ctx context.Context, notice *surface.LoginNotice) {
	loopRecord, err := br.store.Loops().Get(ctx, notice.LoopID)
	if err != nil {
		br.log.Warn("telegram: read loop for login notice", "loop", notice.LoopID, "err", err)
		return
	}
	if loopRecord.TGBotToken == "" || loopRecord.OwnerTGUserID == 0 {
		// not a telegram loop, or one with nobody to tell
		return
	}
	outage := loginOutage{owner: loopRecord.OwnerTGUserID, hostLogin: notice.HostLogin}
	teller, told := br.loginTold[outage]
	if notice.Refused == told {
		// a refusal the owner already heard of, or a login working again
		// that they never heard was refused
		return
	}
	if !notice.Refused {
		delete(br.loginTold, outage)
		br.sendLoginNotice(ctx, teller.loopID, teller.chatID, notice)
		return
	}
	chatID := loopRecord.OwnerDMChatID
	if chatID == 0 {
		br.ledger.LoginNoticeEvent(ctx, notice.LoopID, notice, "the owner has not written to this loop's bot privately yet")
		return
	}
	if br.sendLoginNotice(ctx, notice.LoopID, chatID, notice) {
		br.loginTold[outage] = loginTeller{loopID: notice.LoopID, chatID: chatID}
	}
}

// sendLoginNotice queues a login notice on a loop's bot and records on the
// loop's timeline whether it was, reporting the same.
func (br *Bridge) sendLoginNotice(ctx context.Context, loopID string, chatID int64, notice *surface.LoginNotice) bool {
	bot := br.poller(loopID)
	if bot == nil {
		br.ledger.LoginNoticeEvent(ctx, loopID, notice, "the loop's bot is not running")
		return false
	}
	if unsent := bot.enqueue(sendReq{chatID: chatID, text: notice.Text()}); unsent != "" {
		br.ledger.LoginNoticeEvent(ctx, loopID, notice, unsent)
		return false
	}
	br.ledger.LoginNoticeEvent(ctx, loopID, notice, "")
	return true
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
func (dedup *dedupLRU) Add(key string) bool {
	dedup.mu.Lock()
	defer dedup.mu.Unlock()
	if dedup.seen[key] {
		return false
	}
	dedup.seen[key] = true
	dedup.order = append(dedup.order, key)
	if len(dedup.order) > dedup.max {
		delete(dedup.seen, dedup.order[0])
		dedup.order = dedup.order[1:]
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

// sleepCtx waits duration, and reports false if ctx ended first.
func sleepCtx(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDur(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
