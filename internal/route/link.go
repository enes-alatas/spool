package route

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// LinkAttempt is a DM to a loop's bot or app that may link its sender to a
// control room user: a link code, or the YES that confirms one.
type LinkAttempt struct {
	Text string
	// Person is the sender, as TGPerson or SlackPerson key them.
	Person string
	// LoopID is the loop whose bot or app the DM reached.
	LoopID string
	// Username, Display and TeamID are how the surface names the sender,
	// kept for the sender a link allows.
	Username string
	Display  string
	TeamID   string
	// Describe, when set, fills in Username and Display for a surface that
	// has to ask for them. It is called only for a code the gate checks.
	Describe func(ctx context.Context) (username, display string)
	// Allowed is whether the sender is an allowed sender already. Anyone
	// else is a stranger, who is answered only when a code is good.
	Allowed bool
}

// LinkPayload is a KindLink item: the user's link on a surface changed to
// State.
type LinkPayload struct {
	UserID  string `json:"user_id"`
	Surface string `json:"surface"`
	// State is LinkWaiting, LinkLinked or LinkUnlinked.
	State string `json:"state"`
}

// The states a LinkPayload carries.
const (
	LinkWaiting  = "waiting"
	LinkLinked   = "linked"
	LinkUnlinked = "unlinked"
)

// LinkCode answers a DM that is a link code, or the YES that confirms one
// (#703, ADR-0051). A user asks the control room for a code and sends it
// to any loop's bot or app on the surface it was minted for; the bot asks
// the sender to confirm, and their YES links them and allows them on every
// bot. Neither DM is a message to the loop: it is neither stored nor
// delivered. handled is false for any other text, which the surface takes
// as it would have. notice is what the surface tells the sender in that
// DM, the same words on every surface, and "" when it tells them nothing.
//
// The surface calls it for any sender but a blocked one, ahead of its
// sender gate: a link is how a stranger becomes a sender. A stranger hears
// nothing but the question and the link, and their wrong codes count
// against the limits in linkGate.
func (router *Router) LinkCode(ctx context.Context, attempt LinkAttempt) (notice string, handled bool) {
	code, isCode := users.LinkCodeOf(attempt.Text)
	isYes := isYes(attempt.Text)
	if !isCode && !isYes {
		return "", false
	}
	if router.gate.silenced(attempt.Person, attempt.Allowed) {
		return "", !attempt.Allowed || isCode
	}
	hub := &users.Users{Store: router.store}
	if isYes {
		user, err := hub.ConfirmLink(ctx, attempt.Person)
		switch {
		case errors.Is(err, users.ErrNothingToConfirm) && attempt.Allowed:
			return "", false // a yes to the loop
		case errors.Is(err, users.ErrNothingToConfirm):
			router.wrongCode(attempt)
			return "", true
		case errors.Is(err, users.ErrPersonLinked):
			return router.toAllowed(attempt, personLinkedNotice), true
		case err != nil:
			router.log.Error("link a sender to a user", "err", err)
			return router.toAllowed(attempt, linkFailedNotice), true
		}
		if user.Role == store.RoleOwner {
			router.adoptOwnerless(ctx, attempt.Person)
		}
		PublishSender(ctx, router.store, router.bus, attempt.Person)
		router.publishLink(user.ID, attempt.Person, LinkLinked)
		return "Spool: this account is now linked to " + user.Name + " in the control room, " +
			"and can message every loop here.", true
	}
	if attempt.Describe != nil {
		attempt.Username, attempt.Display = attempt.Describe(ctx)
	}
	user, err := hub.ClaimLink(ctx, code, store.LinkConfirmation{Person: attempt.Person, LoopID: attempt.LoopID,
		Username: attempt.Username, Display: attempt.Display, TeamID: attempt.TeamID})
	switch {
	case errors.Is(err, users.ErrLinkCodeUnknown):
		router.wrongCode(attempt)
		return router.toAllowed(attempt,
			"Spool: that link code is unknown, already used or expired. Ask the control room for a new one."), true
	case errors.Is(err, users.ErrPersonLinked):
		return router.toAllowed(attempt, personLinkedNotice), true
	case err != nil:
		router.log.Error("claim a link code", "err", err)
		return router.toAllowed(attempt, linkFailedNotice), true
	}
	router.publishLink(user.ID, attempt.Person, LinkWaiting)
	return "Spool: this links your " + surfaceTitle(attempt.Person) + " account to " + user.Name +
		" in the control room. " + user.Name + " will read your private conversations with this hub's loops, " +
		"and you can message every one of them. Reply YES within " + minutes(users.LinkConfirmTTL) +
		" to link, or ignore this to leave it unlinked.", true
}

const (
	personLinkedNotice = "Spool: this account is already linked to another control room user, " +
		"who has to remove that link first."
	linkFailedNotice = "Spool: the link failed. Try again, or ask the control room for a new code."
)

// wrongCode counts a wrong code, and logs a warning when it is the one that
// pauses strangers' codes on its surface, so the pause is not silent
// (ADR-0051, #789).
func (router *Router) wrongCode(attempt LinkAttempt) {
	if until, paused := router.gate.wrong(attempt.Person, attempt.Allowed); paused {
		router.log.Warn("link codes from strangers paused: too many wrong codes",
			"surface", store.PersonSurface(attempt.Person), "until", until.UTC().Format(time.RFC3339))
	}
}

// LinkPauseUntil is when strangers' link codes on surface are checked
// again, and the zero time when they are not paused.
func (router *Router) LinkPauseUntil(surface string) time.Time {
	gate := &router.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.pausedUntil(surface, gate.clock())
}

// LiftLinkPause undoes a pause on surface: it zeroes the surface's count of
// strangers' wrong codes, so their codes there are checked again at once
// (ADR-0051, #789). A surface not paused keeps its count, so a lift hands
// no spray under the cap fresh guesses. Each sender's own count stays.
func (router *Router) LiftLinkPause(surface string) {
	gate := &router.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.pausedUntil(surface, gate.clock()).IsZero() {
		return
	}
	delete(gate.strangersWrong, surface)
	router.log.Info("link codes from strangers resumed: pause lifted", "surface", surface)
}

// toAllowed is notice for an allowed sender, and nothing for a stranger:
// a reply to a stranger's wrong code tells them the bot is there and that
// they guessed wrong.
func (router *Router) toAllowed(attempt LinkAttempt, notice string) string {
	if !attempt.Allowed {
		return ""
	}
	return notice
}

// adoptOwnerless makes a Telegram person a link has just allowed the owner
// of every loop with none, as an admin allowing a sender does (#73). Only
// an owner's link does: the admin's choice of sender was the safeguard, a
// link is self-service, and a member who linked first would take every
// ownerless loop's owner DM (ADR-0051).
func (router *Router) adoptOwnerless(ctx context.Context, person string) {
	if store.PersonSurface(person) != store.SurfaceTelegram {
		return
	}
	tgUserID, err := strconv.ParseInt(strings.TrimPrefix(person, "tg:"), 10, 64)
	if err != nil {
		return
	}
	if err := router.store.Loops().AdoptOwner(ctx, tgUserID, time.Now().UnixMilli()); err != nil {
		router.log.Error("default owner", "err", err)
	}
}

// PublishSender tells Settings → Users and access what became of the
// person's sender row after a link changed it: its frame, or that it is gone.
func PublishSender(ctx context.Context, st store.Store, publisher *bus.Bus, person string) {
	surface := store.PersonSurface(person)
	_, id, _ := strings.Cut(person, ":")
	var frame any
	var err error
	switch surface {
	case store.SurfaceTelegram:
		var tgUserID int64
		if tgUserID, err = strconv.ParseInt(id, 10, 64); err != nil {
			return
		}
		var sender *store.TGSender
		if sender, err = st.TGSenders().Get(ctx, tgUserID); err == nil {
			frame = sender.Frame()
		}
	case store.SurfaceSlack:
		var sender *store.SlackSender
		if sender, err = st.SlackSenders().Get(ctx, id); err == nil {
			frame = sender.Frame()
		}
	default:
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		frame = map[string]any{"deleted": id, "surface": surface}
	case err != nil:
		return
	}
	publisher.Publish(bus.Item{Kind: bus.KindAccess, Payload: frame})
}

func (router *Router) publishLink(userID, person, state string) {
	router.bus.Publish(bus.Item{Kind: bus.KindLink, Payload: &LinkPayload{
		UserID: userID, Surface: store.PersonSurface(person), State: state}})
}

// PublishUnlink tells the control room the user's link on surface is gone.
func (router *Router) PublishUnlink(userID, surface string) {
	router.bus.Publish(bus.Item{Kind: bus.KindLink, Payload: &LinkPayload{UserID: userID, Surface: surface, State: LinkUnlinked}})
}

// Stranger notes an event from a sender who is neither allowed nor
// blocked: a DM, a group message, a reaction or a vote, which goes nowhere
// and is answered with nothing. A message's text is untrusted and may hold
// anything, credentials included, so it is never kept or logged: the log
// has one line a sender an hour, with how many of their events were
// dropped (#788). A DM, dm, is a knock: the tally of strangers ignored
// notes who sent it. Anything else is someone in a room the bot sits in,
// and is only logged.
func (router *Router) Stranger(person string, dm bool) {
	router.dropped(person, dm, "events from a stranger ignored")
}

// Blocked notes an event from a blocked sender, which reaches nothing
// (ADR-0051). It is logged as a stranger's is, one line a sender an hour
// with a count, so a blocked sender cannot make the hub log once an event
// (#788). A blocked sender's DM is no knock: they are known already.
func (router *Router) Blocked(person string) {
	router.dropped(person, false, "events from a blocked sender ignored")
}

// dropped counts an event from person and logs it as line when its hour
// has come. The senders a full tally has no room for are logged together,
// with no surface, since they may be on either.
func (router *Router) dropped(person string, dm bool, line string) {
	sender, count, ok := router.gate.drop(person, dm)
	switch {
	case !ok:
	case sender == droppedOthers:
		router.log.Info("events from other senders who are not allowed ignored", "sender", sender, "count", count)
	default:
		router.log.Info(line, "surface", store.PersonSurface(person), "sender", sender, "count", count)
	}
}

func isYes(text string) bool {
	return strings.EqualFold(strings.Trim(text, " \t\r\n.!"), "yes")
}

func surfaceTitle(person string) string {
	if store.PersonSurface(person) == store.SurfaceSlack {
		return "Slack"
	}
	return "Telegram"
}

func minutes(d time.Duration) string {
	return strconv.Itoa(int(d/time.Minute)) + " minutes"
}

// The limits on wrong link codes (ADR-0051). A code is about 49 random
// bits that live 15 minutes, so guessing one is not the danger; a spray of
// guesses is a store read each, and the answer to each tells a code that
// works from one that does not. A sender with linkWrongPerSender wrong
// codes in an hour is not listened to for an hour after the last; past
// linkWrongPerSurface wrong codes from strangers on one surface in an
// hour, no stranger's code there is checked until the hour is out or an
// admin lifts the pause (#789). A YES with nothing to confirm is a wrong
// code. The counts live in memory, so a restart forgets them.
const (
	linkWrongPerSender  = 5
	linkWrongPerSurface = 50
	linkWrongWindow     = time.Hour
	// droppedLogEvery is how often the events dropped from a sender who is
	// not allowed are logged, with a count.
	droppedLogEvery = time.Hour
	// linkGateKept is how many senders the gate holds before it forgets
	// the ones whose hour is out, and the most it holds of dropped events'
	// senders.
	linkGateKept = 1024
	// droppedOthers is who the log names for the events of the senders a
	// full tally did not keep.
	droppedOthers = "others"
	// IgnoredWindow is how far back the tally of ignored strangers
	// reaches, and ignoredKept how many of them it names on a surface: past
	// that, a new one there is not kept, and the tally says there were
	// more. Per surface, so a spray on one hides no knock on the other.
	IgnoredWindow = 24 * time.Hour
	ignoredKept   = 1000
)

// linkGate counts wrong link codes and the events dropped from senders who
// are not allowed. The zero value is ready.
type linkGate struct {
	mu sync.Mutex
	// now is the clock; nil is time.Now.
	now func() time.Time
	// senders counts each sender's wrong codes since since, and until is
	// when one silenced stops being so.
	senders map[string]*linkStrikes
	// strangersWrong counts strangers' wrong codes on each surface, since
	// the first of the hour.
	strangersWrong map[string]*linkStrikes
	// dropped counts each not-allowed sender's dropped events since the
	// last line about them, logged at loggedAt; droppedLeftOut counts those
	// of the senders it had no room for. droppedSwept is when a full tally
	// last forgot the senders whose hour was out: at most once a minute,
	// so a spray of new senders does not scan it on every event (#788).
	dropped        map[string]*droppedTally
	droppedLeftOut droppedTally
	droppedSwept   time.Time
	// ignored is when each stranger of the last IgnoredWindow last sent a
	// DM that was dropped, for the owner to see who is knocking (#744), and
	// ignoredOn how many of them each surface holds. ignoredMore is when a
	// surface last left one out, being full.
	ignored     map[string]time.Time
	ignoredOn   map[string]int
	ignoredMore map[string]time.Time
	// ignoredSwept is when a full tally last forgot the strangers out of
	// the window: at most once a minute, so a spray of new strangers does
	// not scan it on every message (#788).
	ignoredSwept time.Time
}

type linkStrikes struct {
	count int
	since time.Time
	until time.Time
}

type droppedTally struct {
	count    int
	loggedAt time.Time
}

func (gate *linkGate) clock() time.Time {
	if gate.now != nil {
		return gate.now()
	}
	return time.Now()
}

// silenced is whether person's link codes and YESes go unheard now.
func (gate *linkGate) silenced(person string, allowed bool) bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.clock()
	if strikes := gate.senders[person]; strikes != nil && now.Before(strikes.until) {
		return true
	}
	return !allowed && !gate.pausedUntil(store.PersonSurface(person), now).IsZero()
}

// pausedUntil is when strangers' codes on surface are checked again, and
// the zero time when they are not paused.
func (gate *linkGate) pausedUntil(surface string, now time.Time) time.Time {
	strikes := gate.strangersWrong[surface]
	if strikes == nil || strikes.count < linkWrongPerSurface {
		return time.Time{}
	}
	if until := strikes.since.Add(linkWrongWindow); now.Before(until) {
		return until
	}
	return time.Time{}
}

// wrong counts a wrong code from person, and when it is the stranger's code
// that pauses its surface, says until when.
func (gate *linkGate) wrong(person string, allowed bool) (until time.Time, paused bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.clock()
	if gate.senders == nil {
		gate.senders = map[string]*linkStrikes{}
	}
	if len(gate.senders) >= linkGateKept {
		for kept, strikes := range gate.senders {
			if !now.Before(strikes.since.Add(linkWrongWindow)) && !now.Before(strikes.until) {
				delete(gate.senders, kept)
			}
		}
	}
	strikes := gate.senders[person]
	if strikes == nil || !now.Before(strikes.since.Add(linkWrongWindow)) {
		strikes = &linkStrikes{since: now}
		gate.senders[person] = strikes
	}
	strikes.count++
	if strikes.count >= linkWrongPerSender {
		strikes.until = now.Add(linkWrongWindow)
	}
	if allowed {
		return time.Time{}, false
	}
	if gate.strangersWrong == nil {
		gate.strangersWrong = map[string]*linkStrikes{}
	}
	surface := store.PersonSurface(person)
	onSurface := gate.strangersWrong[surface]
	if onSurface == nil || !now.Before(onSurface.since.Add(linkWrongWindow)) {
		onSurface = &linkStrikes{since: now}
		gate.strangersWrong[surface] = onSurface
	}
	onSurface.count++
	if onSurface.count == linkWrongPerSurface {
		return gate.pausedUntil(surface, now), true
	}
	return time.Time{}, false
}

// drop counts a dropped event from person, and says whether to log it:
// as which sender, with the count since the last line. The tally holds
// linkGateKept senders. Once full, it forgets the ones whose hour is out,
// at most once a minute, and while it is still full it keeps no new
// sender: their events are counted with the others left out, logged as
// one sender, droppedOthers (#788).
func (gate *linkGate) drop(person string, dm bool) (string, int, bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.clock()
	if dm {
		gate.ignore(person, now)
	}
	if gate.dropped == nil {
		gate.dropped = map[string]*droppedTally{}
	}
	tally := gate.dropped[person]
	if tally == nil && len(gate.dropped) >= linkGateKept && now.Sub(gate.droppedSwept) >= time.Minute {
		for kept, held := range gate.dropped {
			if !now.Before(held.loggedAt.Add(droppedLogEvery)) {
				delete(gate.dropped, kept)
			}
		}
		gate.droppedSwept = now
	}
	sender := person
	switch {
	case tally != nil:
	case len(gate.dropped) < linkGateKept:
		tally = &droppedTally{}
		gate.dropped[person] = tally
	default:
		tally, sender = &gate.droppedLeftOut, droppedOthers
	}
	tally.count++
	if tally.loggedAt.IsZero() || !now.Before(tally.loggedAt.Add(droppedLogEvery)) {
		count := tally.count
		tally.count, tally.loggedAt = 0, now
		return sender, count, true
	}
	return "", 0, false
}

// ignore notes that person's DM was dropped at now. Once their surface's
// tally is full, it forgets the strangers out of the window, at most once a
// minute, and while it is still full it keeps no new one: it only notes
// that it left one out. gate.mu is held.
func (gate *linkGate) ignore(person string, now time.Time) {
	if gate.ignored == nil {
		gate.ignored, gate.ignoredOn, gate.ignoredMore = map[string]time.Time{}, map[string]int{}, map[string]time.Time{}
	}
	surface := store.PersonSurface(person)
	if _, kept := gate.ignored[person]; !kept {
		if gate.ignoredOn[surface] >= ignoredKept && now.Sub(gate.ignoredSwept) >= time.Minute {
			gate.forgetIgnored(now)
			gate.ignoredSwept = now
		}
		if gate.ignoredOn[surface] >= ignoredKept {
			gate.ignoredMore[surface] = now
			return
		}
		gate.ignoredOn[surface]++
	}
	gate.ignored[person] = now
}

// forgetIgnored drops the strangers last ignored before the window.
// gate.mu is held.
func (gate *linkGate) forgetIgnored(now time.Time) {
	for person, at := range gate.ignored {
		if now.Sub(at) > IgnoredWindow {
			delete(gate.ignored, person)
			gate.ignoredOn[store.PersonSurface(person)]--
		}
	}
}

// IgnoredStrangers are the strangers one surface ignored in the last
// IgnoredWindow: their ids, the most recently ignored first, and whether
// there were more than the tally keeps.
type IgnoredStrangers struct {
	Surface string
	IDs     []string
	More    bool
}

// IgnoredStrangers is who each surface ignored in the last day, by
// surface: senders neither allowed nor blocked, whose DMs went nowhere
// (ADR-0051). Only their ids are kept, never what they said, and
// in memory, so a restart forgets them.
func (router *Router) IgnoredStrangers() []IgnoredStrangers {
	gate := &router.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.clock()
	gate.forgetIgnored(now)
	type seen struct {
		id string
		at time.Time
	}
	bySurface := map[string][]seen{}
	for person, at := range gate.ignored {
		surface := store.PersonSurface(person)
		_, id, _ := strings.Cut(person, ":")
		bySurface[surface] = append(bySurface[surface], seen{id, at})
	}
	all := make([]IgnoredStrangers, 0, 2)
	for _, surface := range []string{store.SurfaceTelegram, store.SurfaceSlack} {
		strangers := bySurface[surface]
		sort.Slice(strangers, func(i, j int) bool { return strangers[i].at.After(strangers[j].at) })
		leftOut, ok := gate.ignoredMore[surface]
		ignored := IgnoredStrangers{Surface: surface, IDs: make([]string, 0, len(strangers)),
			More: ok && now.Sub(leftOut) <= IgnoredWindow}
		for _, stranger := range strangers {
			ignored.IDs = append(ignored.IDs, stranger.id)
		}
		all = append(all, ignored)
	}
	return all
}
