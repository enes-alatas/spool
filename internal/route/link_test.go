package route

import (
	"bytes"
	"context"
	"log/slog"
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
// silence every stranger for the rest of the hour: their codes are not
// even looked up. An allowed sender is still heard past the hub's cap.
func TestWrongCodesSilenceTheSenderAndThenTheHub(t *testing.T) {
	router, links, now, _, _ := linkRouter(t)
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

	for i := range linkWrongPerHub {
		router.LinkCode(ctx, LinkAttempt{Text: "link-aaaaaaaaaa", Person: store.TGPerson(int64(100 + i))})
	}
	before = links.claims
	late := LinkAttempt{Text: mint(t, links, "u-bob", store.SurfaceTelegram), Person: store.TGPerson(7)}
	if notice, _ := router.LinkCode(ctx, late); notice != "" || links.claims != before {
		t.Fatalf("past the hub's cap, a stranger's good code = %q; want nothing looked up", notice)
	}
	late.Allowed = true
	if notice, _ := router.LinkCode(ctx, late); !strings.Contains(notice, "Reply YES") {
		t.Fatalf("past the hub's cap, an allowed sender's good code = %q; want the question", notice)
	}
}

// A stranger's messages are logged once an hour each, with a count and
// never their text.
func TestAStrangerIsLoggedOnceAnHour(t *testing.T) {
	router, _, now, logged, _ := linkRouter(t)
	for range 3 {
		router.Stranger(store.TGPerson(5))
	}
	*now = now.Add(strangerLogEvery)
	router.Stranger(store.TGPerson(5))
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "count=1") || !strings.Contains(lines[1], "count=3") ||
		!strings.Contains(lines[1], "sender=tg:5") {
		t.Fatalf("the log = %q; want the first message, then the next three an hour on", lines)
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
