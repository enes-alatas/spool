package users

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// testToken is the operator token the tests' token sessions open with.
const testToken = "synthetic-operator-token"

// clock is a settable time for the throttle and session expiry.
type clock struct{ at time.Time }

func (fake *clock) now() time.Time { return fake.at }

func newUsers(t *testing.T) (*Users, *clock) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := &clock{at: time.UnixMilli(1_800_000_000_000)}
	return &Users{Store: db, Now: fake.now}, fake
}

func TestPasswordHashRoundTrips(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Fatalf("hash = %q; want the self-describing PBKDF2 form", hash)
	}
	if !CheckPassword(hash, "correct horse battery") || CheckPassword(hash, "correct horse batterY") {
		t.Fatal("CheckPassword does not tell the password from a near miss")
	}
	if CheckPassword("plain", "plain") {
		t.Fatal("a malformed hash checked as a match")
	}
}

func TestValidPassword(t *testing.T) {
	for _, test := range []struct {
		password string
		want     error
	}{
		{strings.Repeat("a", 11), ErrPasswordTooShort},
		{strings.Repeat("a", 12), nil},
		{strings.Repeat("é", 12), nil}, // characters, not bytes
		{strings.Repeat("a", 128), nil},
		{strings.Repeat("a", 129), ErrPasswordTooLong},
	} {
		if got := ValidPassword(test.password); !errors.Is(got, test.want) {
			t.Errorf("ValidPassword(%d chars) = %v; want %v", len([]rune(test.password)), got, test.want)
		}
	}
}

func TestOneTimePasswordReadsOffATerminal(t *testing.T) {
	password, err := NewOneTimePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(password) != oneTimeLength || ValidPassword(password) != nil {
		t.Fatalf("one-time password %q is not a valid %d-character password", password, oneTimeLength)
	}
	if strings.ContainsAny(password, "01ilo") {
		t.Fatalf("one-time password %q holds a character a person misreads", password)
	}
}

func TestLockFor(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		4: 0, 5: 30 * time.Second, 6: time.Minute, 7: 2 * time.Minute, 9: 8 * time.Minute, 10: 15 * time.Minute, 40: 15 * time.Minute,
	} {
		if got := lockFor(failures); got != want {
			t.Errorf("lockFor(%d) = %s; want %s", failures, got, want)
		}
	}
}

// TestSignInThrottlesPerName: five failures lock the name, a locked name
// is refused even with the right password, the lock passes, and a success
// clears the count. An unknown name reads as a wrong password.
func TestSignInThrottlesPerName(t *testing.T) {
	ctx := context.Background()
	users, fake := newUsers(t)
	password, err := users.Add(ctx, "dana", store.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.SignIn(ctx, "nobody", password); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("an unknown name = %v; want ErrBadCredentials", err)
	}
	for range 5 {
		if _, err := users.SignIn(ctx, "dana", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("a wrong password = %v; want ErrBadCredentials", err)
		}
	}
	var throttled ThrottledError
	if _, err := users.SignIn(ctx, "dana", password); !errors.As(err, &throttled) || throttled.RetryAfter != 30*time.Second {
		t.Fatalf("the right password on a locked name = %v; want a 30s ThrottledError", err)
	}
	fake.at = fake.at.Add(31 * time.Second)
	if _, err := users.SignIn(ctx, "dana", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
		t.Fatal(err)
	}
	if _, err := users.SignIn(ctx, "dana", password); !errors.As(err, &throttled) || throttled.RetryAfter != time.Minute {
		t.Fatalf("a sixth failure = %v; want the lock doubled to a minute", err)
	}
	fake.at = fake.at.Add(time.Minute)
	user, err := users.SignIn(ctx, "dana", password)
	if err != nil || !user.MustChangePassword {
		t.Fatalf("the right password after the lock = %+v, %v; want the user, change due", user, err)
	}
	if throttle, err := users.Store.SignInThrottles().Get(ctx, "dana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a success left the throttle at %+v, %v", throttle, err)
	}
}

// TestAnUnknownNameLocksAsARealOneDoes: the throttle is kept by the name
// tried, so five wrong guesses lock a name nobody has exactly as they lock
// a user's, and the lock does not tell the two apart.
func TestAnUnknownNameLocksAsARealOneDoes(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	if _, err := users.Add(ctx, "dana", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dana", "nobody"} {
		for range 5 {
			if _, err := users.SignIn(ctx, name, "wrong password!"); !errors.Is(err, ErrBadCredentials) {
				t.Fatalf("a wrong guess at %s = %v; want ErrBadCredentials", name, err)
			}
		}
		var throttled ThrottledError
		if _, err := users.SignIn(ctx, name, "wrong password!"); !errors.As(err, &throttled) || throttled.RetryAfter != 30*time.Second {
			t.Fatalf("a sixth guess at %s = %v; want a 30s ThrottledError", name, err)
		}
	}
}

// TestFailuresArrivingTogetherEachCount: wrong guesses sent at once each
// count, rather than all reading the same count and writing it back plus
// one, so guessing in parallel does not slip under the lock.
func TestFailuresArrivingTogetherEachCount(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	if _, err := users.Add(ctx, "dana", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	const guesses = 8
	var wg sync.WaitGroup
	for range guesses {
		wg.Go(func() {
			if _, err := users.SignIn(ctx, "dana", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
				t.Errorf("a wrong guess = %v; want ErrBadCredentials", err)
			}
		})
	}
	wg.Wait()
	throttle, err := users.Store.SignInThrottles().Get(ctx, "dana")
	if err != nil || throttle.Failures != guesses {
		t.Fatalf("after %d guesses at once the throttle is %+v, %v; want each counted", guesses, throttle, err)
	}
	if want := lockFor(guesses); time.UnixMilli(throttle.LockedUntil).Sub(users.now()) != want {
		t.Fatalf("the lock holds until %d; want %s from now", throttle.LockedUntil, want)
	}
}

// TestAWrongCurrentPasswordCountsAgainstTheName: once no change is due, a
// session guessing the current password is throttled as a sign-in is, and
// the right one clears the count.
func TestAWrongCurrentPasswordCountsAgainstTheName(t *testing.T) {
	ctx := context.Background()
	users, fake := newUsers(t)
	oneTime, err := users.Add(ctx, "dana", store.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	user, err := users.SignIn(ctx, "dana", oneTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.ChangePassword(ctx, user, "", "a new password of mine", ""); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := users.ChangePassword(ctx, user, "not it", "a third password here", ""); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("a wrong current password = %v; want ErrBadCredentials", err)
		}
	}
	var throttled ThrottledError
	if err := users.ChangePassword(ctx, user, "a new password of mine", "a third password here", ""); !errors.As(err, &throttled) {
		t.Fatalf("the right current password on a locked name = %v; want a ThrottledError", err)
	}
	if _, err := users.SignIn(ctx, "dana", "a new password of mine"); !errors.As(err, &throttled) {
		t.Fatalf("signing in on the name the change locked = %v; want a ThrottledError", err)
	}
	fake.at = fake.at.Add(31 * time.Second)
	if err := users.ChangePassword(ctx, user, "a new password of mine", "a third password here", ""); err != nil {
		t.Fatal(err)
	}
	if throttle, err := users.Store.SignInThrottles().Get(ctx, "dana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a change left the throttle at %+v, %v", throttle, err)
	}
}

// TestATokenSessionEndsWithTheToken: a session the operator token opened
// is refused, and ended, once the hub has a different token.
func TestATokenSessionEndsWithTheToken(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	session, err := users.StartTokenSession(ctx, testToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := users.Session(ctx, session, "synthetic-replaced-token"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a token session under a new token = %v; want ErrSessionNotFound", err)
	}
	if _, _, err := users.Session(ctx, session, testToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("the refused session = %v; want it ended, not only refused", err)
	}
}

// TestSweepForgetsWhatHasRunOut: sessions past either limit, and throttle
// counts a day stale, go without their cookie or name coming back; the
// rest stay.
func TestSweepForgetsWhatHasRunOut(t *testing.T) {
	ctx := context.Background()
	users, fake := newUsers(t)
	if _, err := users.Add(ctx, "dana", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	dana, _ := users.Store.Users().GetByName(ctx, "dana")
	idle, _ := users.StartSession(ctx, dana.ID)
	if _, err := users.SignIn(ctx, "nobody", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
		t.Fatal(err)
	}
	fake.at = fake.at.Add(15 * 24 * time.Hour)
	fresh, _ := users.StartSession(ctx, dana.ID)
	if _, err := users.SignIn(ctx, "someone", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
		t.Fatal(err)
	}
	if err := users.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Store.UserSessions().Get(ctx, sha256Hex(idle)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a session idle 15 days survived the sweep: %v", err)
	}
	if _, err := users.Store.UserSessions().Get(ctx, sha256Hex(fresh)); err != nil {
		t.Fatalf("a fresh session was swept: %v", err)
	}
	if _, err := users.Store.SignInThrottles().Get(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a throttle count 15 days stale survived the sweep: %v", err)
	}
	if _, err := users.Store.SignInThrottles().Get(ctx, "someone"); err != nil {
		t.Fatalf("a fresh throttle count was swept: %v", err)
	}
}

// TestChangePasswordEndsTheOtherSessions: a due change may not reuse the
// one-time password, takes no current password, and keeps only the
// session that made it; a later change needs the current password.
func TestChangePasswordEndsTheOtherSessions(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	oneTime, err := users.Add(ctx, "dana", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	user, err := users.SignIn(ctx, "dana", oneTime)
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := users.StartSession(ctx, user.ID)
	other, _ := users.StartSession(ctx, user.ID)

	if err := users.ChangePassword(ctx, user, "", oneTime, keep); !errors.Is(err, ErrPasswordReused) {
		t.Fatalf("reusing the one-time password = %v; want ErrPasswordReused", err)
	}
	if err := users.ChangePassword(ctx, user, "", "too short", keep); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("a short password = %v; want ErrPasswordTooShort", err)
	}
	if err := users.ChangePassword(ctx, user, "", "a new password of mine", keep); err != nil {
		t.Fatal(err)
	}
	if _, _, err := users.Session(ctx, keep, testToken); err != nil {
		t.Fatalf("the session that changed the password ended: %v", err)
	}
	if _, _, err := users.Session(ctx, other, testToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("another session survived the change: %v", err)
	}
	if err := users.ChangePassword(ctx, user, "not it", "a third password here", keep); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("a change with a wrong current password = %v; want ErrBadCredentials", err)
	}
	if _, err := users.SignIn(ctx, "dana", "a new password of mine"); err != nil {
		t.Fatalf("the new password does not sign in: %v", err)
	}
}

// TestSessionsExpire: unused past the idle limit, or past the absolute
// limit however used, a session is gone; a token session has no user.
func TestSessionsExpire(t *testing.T) {
	ctx := context.Background()
	users, fake := newUsers(t)
	if _, err := users.Add(ctx, "dana", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	dana, _ := users.Store.Users().GetByName(ctx, "dana")

	idle, _ := users.StartSession(ctx, dana.ID)
	busy, _ := users.StartSession(ctx, dana.ID)
	token, _ := users.StartTokenSession(ctx, testToken)
	for range 4 {
		fake.at = fake.at.Add(7 * 24 * time.Hour)
		if _, _, err := users.Session(ctx, busy, testToken); err != nil {
			t.Fatalf("a session used weekly ended at %s: %v", fake.at, err)
		}
		if _, user, err := users.Session(ctx, token, testToken); err != nil || user != nil {
			t.Fatalf("token session = %+v, %v; want it alive and userless", user, err)
		}
	}
	if _, _, err := users.Session(ctx, idle, testToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a session unused for 28 days = %v; want it ended", err)
	}
	fake.at = fake.at.Add(3 * 24 * time.Hour)
	if _, _, err := users.Session(ctx, busy, testToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a session 31 days old = %v; want it ended", err)
	}
}

// TestBootstrapResetAndRemove: the first bootstrap creates owner admin and
// only the first; a reset ends the user's sessions and makes a change due;
// the last owner stays.
func TestBootstrapResetAndRemove(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	password, err := users.Bootstrap(ctx)
	if err != nil || password == "" {
		t.Fatalf("first bootstrap = %q, %v; want a one-time password", password, err)
	}
	if again, err := users.Bootstrap(ctx); err != nil || again != "" {
		t.Fatalf("second bootstrap = %q, %v; want nothing", again, err)
	}
	admin, err := users.SignIn(ctx, AdminName, password)
	if err != nil || admin.Role != store.RoleOwner {
		t.Fatalf("admin = %+v, %v; want the owner", admin, err)
	}
	if err := users.ChangePassword(ctx, admin, "", "admin's own password", ""); err != nil {
		t.Fatal(err)
	}
	session, _ := users.StartSession(ctx, admin.ID)
	reset, err := users.Reset(ctx, AdminName)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := users.Session(ctx, session, testToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a session survived the reset: %v", err)
	}
	if admin, err = users.SignIn(ctx, AdminName, reset); err != nil || !admin.MustChangePassword {
		t.Fatalf("after a reset = %+v, %v; want a change due", admin, err)
	}

	if err := users.Remove(ctx, AdminName); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("removing the last owner = %v; want ErrLastOwner", err)
	}
	if _, err := users.Add(ctx, "second", store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := users.Remove(ctx, AdminName); err != nil {
		t.Fatalf("removing one of two owners: %v", err)
	}
	if _, err := users.Add(ctx, "Bad Name", store.RoleMember); !errors.Is(err, ErrBadName) {
		t.Fatalf("a bad name = %v", err)
	}
	if _, err := users.Add(ctx, "second", store.RoleMember); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("a taken name = %v", err)
	}
}

// TestSetRoleKeepsAnOwner: a role changes to any of the three, but the
// hub's only owner stays one, and a role or a name the hub does not know
// is refused.
func TestSetRoleKeepsAnOwner(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	if _, err := users.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if err := users.SetRole(ctx, AdminName, store.RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demoting the last owner = %v; want ErrLastOwner", err)
	}
	if err := users.SetRole(ctx, AdminName, store.RoleOwner); err != nil {
		t.Fatalf("keeping the last owner an owner = %v", err)
	}
	if _, err := users.Add(ctx, "dana", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := users.SetRole(ctx, "dana", store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := users.SetRole(ctx, AdminName, store.RoleMember); err != nil {
		t.Fatalf("demoting one of two owners = %v", err)
	}
	if err := users.SetRole(ctx, "dana", store.RoleAdmin); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demoting the owner left = %v; want ErrLastOwner", err)
	}
	if err := users.Remove(ctx, "dana"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("removing the owner left = %v; want ErrLastOwner", err)
	}
	if dana, err := users.Store.Users().GetByName(ctx, "dana"); err != nil || dana.Role != store.RoleOwner {
		t.Fatalf("dana = %+v, %v; want the owner still", dana, err)
	}
	if err := users.SetRole(ctx, "dana", "superuser"); !errors.Is(err, ErrBadRole) {
		t.Fatalf("an unknown role = %v; want ErrBadRole", err)
	}
	if err := users.SetRole(ctx, "nobody", store.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown name = %v; want ErrNotFound", err)
	}
}

// TestConfirmCountsAsASignIn: a wrong password confirmed in a session
// counts toward the name's lock, and a right one clears the count.
func TestConfirmCountsAsASignIn(t *testing.T) {
	ctx := context.Background()
	users, _ := newUsers(t)
	oneTime, err := users.Add(ctx, "dana", store.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	dana, err := users.SignIn(ctx, "dana", oneTime)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := users.Confirm(ctx, dana, "not it"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("a wrong password = %v; want ErrBadCredentials", err)
		}
	}
	if err := users.Confirm(ctx, dana, oneTime); err != nil {
		t.Fatalf("the right password = %v", err)
	}
	for range 5 {
		if err := users.Confirm(ctx, dana, "not it"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("a wrong password after a right one = %v; want ErrBadCredentials", err)
		}
	}
	var throttled ThrottledError
	if err := users.Confirm(ctx, dana, oneTime); !errors.As(err, &throttled) {
		t.Fatalf("the right password on a locked name = %v; want a ThrottledError", err)
	}
}
