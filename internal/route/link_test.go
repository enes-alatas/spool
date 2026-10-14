package route

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/users"
)

// memLinks keeps link codes and confirmations as the store's contract
// says, without links themselves beyond who holds a person.
type memLinks struct {
	store.UserLinkStore
	codes         map[string]memCode
	confirmations map[string]*store.LinkConfirmation
	linked        map[string]string // person → user ID
	claims        int
	adopted       []int64
}

type memCode struct{ userID, surface string }

func (links *memLinks) SetCode(_ context.Context, userID, surface, codeHash string, _ int64) error {
	links.codes[codeHash] = memCode{userID, surface}
	return nil
}

func (links *memLinks) Claim(_ context.Context, codeHash string, confirmation *store.LinkConfirmation, _ int64) error {
	links.claims++
	code, ok := links.codes[codeHash]
	delete(links.codes, codeHash)
	if !ok || code.surface != store.PersonSurface(confirmation.Person) {
		return store.ErrNotFound
	}
	confirmation.UserID = code.userID
	links.confirmations[confirmation.Person] = confirmation
	return nil
}

func (links *memLinks) Confirm(_ context.Context, person string, _ int64) (*store.UserLink, error) {
	confirmation, ok := links.confirmations[person]
	if !ok {
		return nil, store.ErrNotFound
	}
	delete(links.confirmations, person)
	links.linked[person] = confirmation.UserID
	return &store.UserLink{UserID: confirmation.UserID, Person: person}, nil
}

// memUsers knows every user; alice is the hub's owner and the rest are
// members.
type memUsers struct{ store.UserStore }

func (memUsers) Get(_ context.Context, id string) (*store.User, error) {
	role := store.RoleMember
	if id == "u-alice" {
		role = store.RoleOwner
	}
	return &store.User{ID: id, Name: strings.TrimPrefix(id, "u-"), Role: role}, nil
}

// noSenders knows no Telegram sender, so a link's Access frame says the
// row is gone.
type noSenders struct{ store.TGSenderStore }

func (noSenders) Get(context.Context, int64) (*store.TGSender, error) { return nil, store.ErrNotFound }

// memLoops records who was made the owner of the ownerless loops.
type memLoops struct {
	store.LoopStore
	adopted *[]int64
}

func (loops memLoops) AdoptOwner(_ context.Context, tgUserID, _ int64) error {
	*loops.adopted = append(*loops.adopted, tgUserID)
	return nil
}

type linksOnly struct {
	store.Store
	links   *memLinks
	adopted *[]int64
}

func (fake linksOnly) UserLinks() store.UserLinkStore { return fake.links }
func (linksOnly) Users() store.UserStore              { return memUsers{} }
func (fake linksOnly) Loops() store.LoopStore         { return memLoops{adopted: fake.adopted} }
func (linksOnly) TGSenders() store.TGSenderStore      { return noSenders{} }

// linkRouter is a router over memory links, with a clock the test turns
// and a log it reads.
func linkRouter(t *testing.T) (*Router, *memLinks, *time.Time, *bytes.Buffer, <-chan bus.Item) {
	links := &memLinks{codes: map[string]memCode{}, confirmations: map[string]*store.LinkConfirmation{},
		linked: map[string]string{}}
	publisher := bus.New()
	frames, cancel := publisher.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindLink })
	t.Cleanup(cancel)
	var logged bytes.Buffer
	router := New(linksOnly{links: links, adopted: &links.adopted}, publisher, nil, slog.New(slog.NewTextHandler(&logged, nil)))
	now := time.Unix(1_800_000_000, 0)
	router.gate.now = func() time.Time { return now }
	return router, links, &now, &logged, frames
}

// mint is a fresh code for userID on surface.
func mint(t *testing.T, links *memLinks, userID, surface string) string {
	t.Helper()
	code, _, err := (&users.Users{Store: linksOnly{links: links}}).NewLinkCode(context.Background(), userID, surface)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// A stranger who sends a good code is asked to confirm, naming the user
// who will read their private conversations, and their YES links them.
// Nothing else they send is answered (ADR-0051).
func TestAStrangerLinksByCodeAndYes(t *testing.T) {
	router, links, _, _, frames := linkRouter(t)
	ctx := context.Background()
	stranger := LinkAttempt{Person: store.TGPerson(5), LoopID: "l1"}

	attempt := stranger
	attempt.Text = "hello?"
	if notice, handled := router.LinkCode(ctx, attempt); handled || notice != "" {
		t.Fatalf("a stranger's chat = %q, %v; want it left to the surface", notice, handled)
	}
	attempt.Text = "yes"
	if notice, handled := router.LinkCode(ctx, attempt); !handled || notice != "" {
		t.Fatalf("a stranger's YES with nothing to confirm = %q, %v; want it taken, unanswered", notice, handled)
	}

	attempt.Text = mint(t, links, "u-alice", store.SurfaceTelegram)
	notice, handled := router.LinkCode(ctx, attempt)
	if !handled || !strings.Contains(notice, "Telegram account to alice") ||
		!strings.Contains(notice, "alice will read your private conversations") || !strings.Contains(notice, "Reply YES") {
		t.Fatalf("a stranger's good code = %q, %v; want the question naming alice", notice, handled)
	}
	if frame := (<-frames).Payload.(*LinkPayload); frame.UserID != "u-alice" || frame.State != LinkWaiting {
		t.Fatalf("the room heard %+v, want alice waiting", frame)
	}
	if links.linked[stranger.Person] != "" {
		t.Fatal("a code linked before the YES")
	}
	attempt.Text = " Yes! "
	if notice, handled := router.LinkCode(ctx, attempt); !handled || !strings.Contains(notice, "now linked to alice") {
		t.Fatalf("the YES = %q, %v; want the link", notice, handled)
	}
	if links.linked[stranger.Person] != "u-alice" {
		t.Fatal("the YES linked nobody")
	}
	if len(links.adopted) != 1 || links.adopted[0] != 5 {
		t.Fatalf("the ownerless loops went to %v, want the person just allowed", links.adopted)
	}
	if frame := (<-frames).Payload.(*LinkPayload); frame.UserID != "u-alice" || frame.State != LinkLinked {
		t.Fatalf("the room heard %+v, want alice linked", frame)
	}
}

// A code minted for Slack links nobody on Telegram, and a stranger hears
// nothing of it; an allowed sender is told, and their "yes" with nothing
// to confirm is a message to the loop.
func TestAWrongCodeIsAnsweredOnlyToAnAllowedSender(t *testing.T) {
	router, links, _, _, _ := linkRouter(t)
	ctx := context.Background()
	code := mint(t, links, "u-alice", store.SurfaceSlack)
	if notice, handled := router.LinkCode(ctx, LinkAttempt{Text: code, Person: store.TGPerson(5)}); !handled || notice != "" {
		t.Fatalf("a stranger's slack code on telegram = %q, %v; want it taken, unanswered", notice, handled)
	}
	allowed := LinkAttempt{Text: mint(t, links, "u-alice", store.SurfaceSlack), Person: store.TGPerson(6), Allowed: true}
	if notice, handled := router.LinkCode(ctx, allowed); !handled || !strings.Contains(notice, "unknown, already used or expired") {
		t.Fatalf("an allowed sender's wrong code = %q, %v; want them told", notice, handled)
	}
	allowed.Text = "yes"
	if _, handled := router.LinkCode(ctx, allowed); handled {
		t.Fatal("an allowed sender's yes, with nothing to confirm, did not reach the loop")
	}
}

// Five wrong codes silence a sender for an hour, and fifty from strangers
// on one surface silence every stranger there for the rest of the hour:
// their codes are not even looked up. An allowed sender is still heard, and
// so is a stranger on the other surface (#789). The pause is logged, shown,
// and lifted by an admin.
func TestWrongCodesSilenceTheSenderAndThenTheSurface(t *testing.T) {
	router, links, now, logged, _ := linkRouter(t)
	ctx := context.Background()
	guesser := LinkAttempt{Text: "link-aaaaaaaaaa", Person: store.TGPerson(5)}
	for range linkWrongPerSender {
		router.LinkCode(ctx, guesser)
	}
	guesser.Text = mint(t, links, "u-alice", store.SurfaceTelegram)
	before := links.claims
	if notice, handled := router.LinkCode(ctx, guesser); !handled || notice != "" || links.claims != before {
		t.Fatalf("a silenced sender's good code = %q, %v and %d lookups; want nothing", notice, handled, links.claims-before)
	}
	*now = now.Add(linkWrongWindow)
	if notice, _ := router.LinkCode(ctx, guesser); !strings.Contains(notice, "Reply YES") {
		t.Fatalf("an hour on, the sender's good code = %q; want the question", notice)
	}

	for i := range linkWrongPerSurface {
		if i == linkWrongPerSurface-1 {
			// A lift before the pause keeps the count: the next code pauses.
			router.LiftLinkPause(store.SurfaceTelegram)
		}
		router.LinkCode(ctx, LinkAttempt{Text: "link-aaaaaaaaaa", Person: store.TGPerson(int64(100 + i))})
	}
	until := router.LinkPauseUntil(store.SurfaceTelegram)
	if !until.Equal(now.Add(linkWrongWindow)) {
		t.Fatalf("Telegram's pause runs until %v; want the hour from the first wrong code, %v", until, now.Add(linkWrongWindow))
	}
	if !strings.Contains(logged.String(), "link codes from strangers paused") || !strings.Contains(logged.String(), "surface=telegram") {
		t.Fatalf("the pause was not logged with its surface:\n%s", logged.String())
	}
	before = links.claims
	late := LinkAttempt{Text: mint(t, links, "u-bob", store.SurfaceTelegram), Person: store.TGPerson(7)}
	if notice, _ := router.LinkCode(ctx, late); notice != "" || links.claims != before {
		t.Fatalf("past Telegram's cap, a stranger's good code = %q; want nothing looked up", notice)
	}
	late.Allowed = true
	if notice, _ := router.LinkCode(ctx, late); !strings.Contains(notice, "Reply YES") {
		t.Fatalf("past Telegram's cap, an allowed sender's good code = %q; want the question", notice)
	}
	if paused := router.LinkPauseUntil(store.SurfaceSlack); !paused.IsZero() {
		t.Fatalf("Slack paused until %v by Telegram's wrong codes; want it unpaused", paused)
	}
	slack := LinkAttempt{Text: mint(t, links, "u-carol", store.SurfaceSlack), Person: store.SlackPerson("U0CAROL")}
	if notice, _ := router.LinkCode(ctx, slack); !strings.Contains(notice, "Reply YES") {
		t.Fatalf("past Telegram's cap, a Slack stranger's good code = %q; want the question", notice)
	}

	router.LiftLinkPause(store.SurfaceTelegram)
	if paused := router.LinkPauseUntil(store.SurfaceTelegram); !paused.IsZero() {
		t.Fatalf("after the lift Telegram is paused until %v", paused)
	}
	lifted := LinkAttempt{Text: mint(t, links, "u-dave", store.SurfaceTelegram), Person: store.TGPerson(8)}
	if notice, _ := router.LinkCode(ctx, lifted); !strings.Contains(notice, "Reply YES") {
		t.Fatalf("after the lift, a stranger's good code = %q; want the question", notice)
	}
}

// A stranger's messages are logged once an hour each, with a count and
// never their text.
func TestAStrangerIsLoggedOnceAnHour(t *testing.T) {
	router, _, now, logged, _ := linkRouter(t)
	for range 3 {
		router.Stranger(store.TGPerson(5), false)
	}
	*now = now.Add(droppedLogEvery)
	router.Stranger(store.TGPerson(5), true)
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "count=1") || !strings.Contains(lines[1], "count=3") ||
		!strings.Contains(lines[1], "sender=tg:5") {
		t.Fatalf("the log = %q; want the first message, then the next three an hour on", lines)
	}
}

// A blocked sender's events are logged as a stranger's are, once an hour
// with a count, and are no knock (#788).
func TestABlockedSenderIsLoggedOnceAnHour(t *testing.T) {
	router, _, now, logged, _ := linkRouter(t)
	for range 3 {
		router.Blocked(store.SlackPerson("U6"))
	}
	*now = now.Add(droppedLogEvery)
	router.Blocked(store.SlackPerson("U6"))
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "blocked sender") || !strings.Contains(lines[0], "count=1") ||
		!strings.Contains(lines[1], "count=3") || !strings.Contains(lines[1], "sender=slack:U6") {
		t.Fatalf("the log = %q; want the first event, then the next three an hour on", lines)
	}
	if got := router.IgnoredStrangers(); len(got[1].IDs) != 0 {
		t.Fatalf("the tally = %+v; want no knock from a blocked sender", got)
	}
}

// The tally of senders whose events were dropped holds linkGateKept of
// them. While it is full, a new sender's events are counted and logged
// with the others left out, as one sender, so a spray of new senders
// neither grows it nor logs once each. Once the kept senders' hour is out,
// a new one is kept again (#788).
func TestAFullDroppedTallyCountsTheRestAsOthers(t *testing.T) {
	router, _, now, logged, _ := linkRouter(t)
	for id := range int64(linkGateKept) {
		router.Stranger(store.TGPerson(1000+id), false)
	}
	logged.Reset()
	for id := range int64(3) {
		router.Stranger(store.TGPerson(5000+id), false)
	}
	router.Blocked(store.TGPerson(6000))
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "sender="+droppedOthers) || !strings.Contains(lines[0], "count=1") ||
		strings.Contains(lines[0], "surface=") {
		t.Fatalf("the log past a full tally = %q; want one line for the others", lines)
	}
	if n := len(router.gate.dropped); n != linkGateKept {
		t.Fatalf("the tally holds %d senders, want %d", n, linkGateKept)
	}

	*now = now.Add(droppedLogEvery)
	logged.Reset()
	router.Stranger(store.TGPerson(5000), false)
	router.Stranger(store.TGPerson(7000), false)
	lines = strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "sender=tg:5000") || !strings.Contains(lines[1], "sender=tg:7000") {
		t.Fatalf("the log an hour on = %q; want 5000 and 7000 kept and logged as themselves", lines)
	}
}

// The hub tallies the strangers whose DMs it ignored in the last day, by
// surface, the most recent first; a group message is not a knock and is
// not tallied. A stranger out of the day is forgotten, and past
// ignoredKept on a surface a new one there is not kept but said to be
// there, while the other surface keeps counting (#744).
func TestIgnoredStrangersAreTalliedForADay(t *testing.T) {
	router, _, now, _, _ := linkRouter(t)
	router.Stranger(store.TGPerson(1), true)
	router.Stranger(store.TGPerson(7), false)
	*now = now.Add(time.Hour)
	router.Stranger(store.SlackPerson("U2"), true)
	router.Stranger(store.TGPerson(3), true)
	router.Stranger(store.TGPerson(3), true)
	if got := router.IgnoredStrangers(); len(got) != 2 ||
		strings.Join(got[0].IDs, ",") != "3,1" || got[0].Surface != store.SurfaceTelegram ||
		strings.Join(got[1].IDs, ",") != "U2" || got[0].More || got[1].More {
		t.Fatalf("the tally = %+v; want telegram 3 then 1, slack U2, and not 7's group message", got)
	}

	*now = now.Add(IgnoredWindow - time.Hour + time.Second)
	if got := router.IgnoredStrangers(); strings.Join(got[0].IDs, ",") != "3" {
		t.Fatalf("the tally a day after stranger 1 = %+v; want only 3", got[0])
	}

	for id := range int64(ignoredKept) {
		router.Stranger(store.TGPerson(1000+id), true)
	}
	router.Stranger(store.SlackPerson("U9"), true)
	got := router.IgnoredStrangers()
	if len(got[0].IDs) != ignoredKept || !got[0].More || slices.Contains(got[0].IDs, "1999") ||
		strings.Join(got[1].IDs, ",") != "U9,U2" || got[1].More {
		t.Fatalf("a full telegram tally = %d (more %v), slack %v (more %v); want %d kept, the last left out, and slack untouched",
			len(got[0].IDs), got[0].More, got[1].IDs, got[1].More, ignoredKept)
	}
	*now = now.Add(IgnoredWindow + time.Minute)
	router.Stranger(store.TGPerson(5), true)
	if got := router.IgnoredStrangers(); strings.Join(got[0].IDs, ",") != "5" || got[0].More || len(got[1].IDs) != 0 {
		t.Fatalf("the tally a day on = %+v; want only 5, and no more", got)
	}
}

// A member's link allows them but makes them owner of nothing: a link is
// self-service, and only the hub's owner takes the ownerless loops by one
// (ADR-0051).
func TestAMembersLinkAdoptsNoLoop(t *testing.T) {
	router, links, _, _, _ := linkRouter(t)
	ctx := context.Background()
	attempt := LinkAttempt{Person: store.TGPerson(6), LoopID: "l1", Text: mint(t, links, "u-bob", store.SurfaceTelegram)}
	if _, handled := router.LinkCode(ctx, attempt); !handled {
		t.Fatal("bob's code was not taken")
	}
	attempt.Text = "yes"
	if notice, _ := router.LinkCode(ctx, attempt); !strings.Contains(notice, "now linked to bob") {
		t.Fatalf("the YES = %q, want the link", notice)
	}
	if len(links.adopted) != 0 {
		t.Fatalf("a member's link made %v the owner of the ownerless loops", links.adopted)
	}
}
