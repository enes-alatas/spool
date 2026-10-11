package route

import (
	"context"
	"errors"
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
			router.gate.wrong(attempt.Person, attempt.Allowed)
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
		router.gate.wrong(attempt.Person, attempt.Allowed)
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

// PublishSender tells the Access page what became of the person's sender
// row after a link changed it: its frame, or that it is gone.
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

// Stranger notes a DM or group message from a sender who is not allowed,
// which goes nowhere and is answered with nothing. Its text is untrusted
// and may hold anything, credentials included, so it is never kept or
// logged: the log has one line a sender an hour, with how many of their
// messages were dropped.
func (router *Router) Stranger(person string) {
	if count, ok := router.gate.stranger(person); ok {
		router.log.Info("messages from a stranger ignored", "surface", store.PersonSurface(person),
			"sender", person, "count", count)
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
// linkWrongPerHub wrong codes from strangers in an hour, no stranger's
// code is checked until the hour is out. A YES with nothing to confirm is
// a wrong code. The counts live in memory, so a restart forgets them.
const (
	linkWrongPerSender = 5
	linkWrongPerHub    = 50
	linkWrongWindow    = time.Hour
	// strangerLogEvery is how often a stranger's dropped messages are
	// logged, with a count.
	strangerLogEvery = time.Hour
	// linkGateKept is how many senders the gate holds before it forgets
	// the ones whose hour is out.
	linkGateKept = 1024
)

// linkGate counts wrong link codes and strangers' dropped messages. The
// zero value is ready.
type linkGate struct {
	mu sync.Mutex
	// now is the clock; nil is time.Now.
	now func() time.Time
	// senders counts each sender's wrong codes since since, and until is
	// when one silenced stops being so.
	senders map[string]*linkStrikes
	// hub counts strangers' wrong codes since hubSince.
	hub      int
	hubSince time.Time
	// strangers counts each stranger's dropped messages since the last line
	// about them, logged at loggedAt.
	strangers map[string]*strangerTally
}

type linkStrikes struct {
	count int
	since time.Time
	until time.Time
}

type strangerTally struct {
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
	return !allowed && gate.hub >= linkWrongPerHub && now.Before(gate.hubSince.Add(linkWrongWindow))
}

// wrong counts a wrong code from person.
func (gate *linkGate) wrong(person string, allowed bool) {
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
		return
	}
	if !now.Before(gate.hubSince.Add(linkWrongWindow)) {
		gate.hub, gate.hubSince = 0, now
	}
	gate.hub++
}

// stranger counts a dropped message from person, and says whether to log
// it, with the count since the last line.
func (gate *linkGate) stranger(person string) (int, bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.clock()
	if gate.strangers == nil {
		gate.strangers = map[string]*strangerTally{}
	}
	if len(gate.strangers) >= linkGateKept {
		for kept, tally := range gate.strangers {
			if !now.Before(tally.loggedAt.Add(strangerLogEvery)) {
				delete(gate.strangers, kept)
			}
		}
	}
	tally := gate.strangers[person]
	if tally == nil {
		tally = &strangerTally{}
		gate.strangers[person] = tally
	}
	tally.count++
	if tally.loggedAt.IsZero() || !now.Before(tally.loggedAt.Add(strangerLogEvery)) {
		count := tally.count
		tally.count, tally.loggedAt = 0, now
		return count, true
	}
	return 0, false
}
