// Package users is the hub's users and their sign-in sessions (ADR-0048):
// how a password is kept and checked, the one-time password a new or reset
// user starts with, the per-username throttle, and the sessions the control
// room's cookie names. The store keeps the rows; this package keeps the
// rules, so the API and the CLI cannot each arrive at their own.
package users

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// The password rules (ADR-0048): a length, and no composition rules.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

// AdminName is the owner the hub's first start creates.
const AdminName = "admin"

// PBKDF2-SHA256 as decided: 600,000 iterations over a random 16-byte salt,
// for a 32-byte key.
const (
	hashIterations = 600_000
	saltBytes      = 16
	keyBytes       = 32
	hashScheme     = "pbkdf2-sha256"
)

// Sessions end after this long unused, or this long after sign-in, and
// their use is written at most once per touchEvery.
const (
	SessionIdle     = 14 * 24 * time.Hour
	SessionAbsolute = 30 * 24 * time.Hour
	touchEvery      = time.Minute
)

// The throttle: this many failed sign-ins in a row lock the name for
// firstLock, each further failure doubles it, up to maxLock. A name with
// no failure for throttleMemory starts over.
const (
	failuresBeforeLock = 5
	firstLock          = 30 * time.Second
	maxLock            = 15 * time.Minute
	throttleMemory     = 24 * time.Hour
)

// oneTimeAlphabet leaves out the characters a person misreads: 0 and o, 1,
// l and i.
const (
	oneTimeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	oneTimeLength   = 20
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

var (
	ErrBadName           = errors.New("a user name is 2 to 32 lowercase letters, digits, - or _, starting with a letter or digit")
	ErrBadRole           = errors.New("a role is owner, admin or member")
	ErrPasswordTooShort  = fmt.Errorf("a password is at least %d characters", MinPasswordLength)
	ErrPasswordTooLong   = fmt.Errorf("a password is at most %d characters", MaxPasswordLength)
	ErrPasswordReused    = errors.New("the new password is the one-time password it replaces")
	ErrBadCredentials    = errors.New("wrong username or password")
	ErrLastOwner         = errors.New("the last owner cannot be removed")
	ErrSessionNotFound   = errors.New("no such session")
	errMalformedPassword = errors.New("stored password hash is malformed")
)

// ThrottledError refuses a sign-in to a locked name, and says how long the
// lock still holds.
type ThrottledError struct{ RetryAfter time.Duration }

func (err ThrottledError) Error() string {
	return fmt.Sprintf("too many failed sign-ins; try again in %s", err.RetryAfter.Round(time.Second))
}

// Users applies the rules to the store's rows.
type Users struct {
	Store store.Store
	// Now is the clock; nil is the wall clock.
	Now func() time.Time
}

func (users *Users) now() time.Time {
	if users.Now != nil {
		return users.Now()
	}
	return time.Now()
}

// ValidPassword checks a password against the length rules.
func ValidPassword(password string) error {
	switch length := len([]rune(password)); {
	case length < MinPasswordLength:
		return ErrPasswordTooShort
	case length > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	return nil
}

// ValidRole reports whether role is one of the three.
func ValidRole(role string) bool {
	return role == store.RoleOwner || role == store.RoleAdmin || role == store.RoleMember
}

// HashPassword keeps password as a self-describing PBKDF2 hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, keyBytes)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{hashScheme, strconv.Itoa(hashIterations),
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)}, "$"), nil
}

// CheckPassword reports whether password is the one encoded was made from.
func CheckPassword(encoded, password string) bool {
	ok, err := checkPassword(encoded, password)
	return err == nil && ok
}

func checkPassword(encoded, password string) (bool, error) {
	fields := strings.Split(encoded, "$")
	if len(fields) != 4 || fields[0] != hashScheme {
		return false, errMalformedPassword
	}
	iterations, err := strconv.Atoi(fields[1])
	if err != nil || iterations < 1 {
		return false, errMalformedPassword
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[2])
	if err != nil {
		return false, errMalformedPassword
	}
	want, err := base64.RawStdEncoding.DecodeString(fields[3])
	if err != nil {
		return false, errMalformedPassword
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decoyHash is checked against when a sign-in names no user, so an unknown
// name costs the same hashing as a wrong password and its timing does not
// say which names exist. Made on first use rather than at start, since it
// costs a full hashing.
var decoyHash = sync.OnceValue(func() string {
	hash, _ := HashPassword("decoy password, never anyone's")
	return hash
})

// NewOneTimePassword draws a password a person can read off a terminal.
func NewOneTimePassword() (string, error) {
	out := make([]byte, oneTimeLength)
	random := make([]byte, oneTimeLength)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for i := range out {
		// 256 is not a multiple of the alphabet's size, so redraw the bytes
		// above the largest multiple: a plain modulo would favour the start
		// of the alphabet.
		for int(random[i]) >= 256-256%len(oneTimeAlphabet) {
			if _, err := rand.Read(random[i : i+1]); err != nil {
				return "", err
			}
		}
		out[i] = oneTimeAlphabet[int(random[i])%len(oneTimeAlphabet)]
	}
	return string(out), nil
}

// lockFor is how long failures failed sign-ins in a row lock a name.
func lockFor(failures int) time.Duration {
	if failures < failuresBeforeLock {
		return 0
	}
	lock := firstLock
	for range failures - failuresBeforeLock {
		lock *= 2
		if lock >= maxLock {
			return maxLock
		}
	}
	return lock
}

// Bootstrap creates owner admin when the hub has no users, and returns its
// one-time password; "" when there were users already.
func (users *Users) Bootstrap(ctx context.Context) (string, error) {
	existing, err := users.Store.Users().List(ctx)
	if err != nil || len(existing) > 0 {
		return "", err
	}
	return users.Add(ctx, AdminName, store.RoleOwner)
}

// Add creates a user who must change the one-time password it returns at
// first sign-in.
func (users *Users) Add(ctx context.Context, name, role string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrBadName
	}
	if !ValidRole(role) {
		return "", ErrBadRole
	}
	password, hash, err := oneTimeCredential()
	if err != nil {
		return "", err
	}
	user := &store.User{ID: newID(), Name: name, Role: role, PasswordHash: hash,
		MustChangePassword: true, CreatedAt: users.now().UnixMilli()}
	if err := users.Store.Users().Create(ctx, user); err != nil {
		return "", err
	}
	return password, nil
}

// Reset gives a user a new one-time password, which must be changed at
// their next sign-in, and ends every session they hold.
func (users *Users) Reset(ctx context.Context, name string) (string, error) {
	user, err := users.Store.Users().GetByName(ctx, name)
	if err != nil {
		return "", err
	}
	password, hash, err := oneTimeCredential()
	if err != nil {
		return "", err
	}
	if err := users.Store.Users().SetPassword(ctx, user.ID, hash, true); err != nil {
		return "", err
	}
	if err := users.Store.SignInThrottles().Clear(ctx, name); err != nil {
		return "", err
	}
	return password, users.Store.UserSessions().DeleteForUser(ctx, user.ID, "")
}

// Remove deletes a user and their sessions. The last owner stays, since a
// hub with no owner has nobody who may add one back.
func (users *Users) Remove(ctx context.Context, name string) error {
	user, err := users.Store.Users().GetByName(ctx, name)
	if err != nil {
		return err
	}
	if user.Role == store.RoleOwner {
		all, err := users.Store.Users().List(ctx)
		if err != nil {
			return err
		}
		owners := 0
		for _, other := range all {
			if other.Role == store.RoleOwner {
				owners++
			}
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	return users.Store.Users().Delete(ctx, user.ID)
}

func oneTimeCredential() (password, hash string, err error) {
	if password, err = NewOneTimePassword(); err != nil {
		return "", "", err
	}
	hash, err = HashPassword(password)
	return password, hash, err
}

// SignIn checks a username and password. The throttle is kept by the name
// tried, so an unknown name locks as a real one does. A locked name is
// refused with a ThrottledError, and an unknown name and a wrong password
// are both ErrBadCredentials; each answer comes after the same hashing
// work, so neither it nor its timing shows which names exist.
func (users *Users) SignIn(ctx context.Context, name, password string) (*store.User, error) {
	now := users.now()
	if err := users.locked(ctx, name, now); err != nil {
		CheckPassword(decoyHash(), password)
		return nil, err
	}
	user, err := users.Store.Users().GetByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		CheckPassword(decoyHash(), password)
		return nil, users.fail(ctx, name, now)
	}
	if err != nil {
		return nil, err
	}
	ok, err := checkPassword(user.PasswordHash, password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, users.fail(ctx, name, now)
	}
	if err := users.Store.SignInThrottles().Clear(ctx, name); err != nil {
		return nil, err
	}
	return user, nil
}

// locked is a ThrottledError while name is locked at now, else nil.
func (users *Users) locked(ctx context.Context, name string, now time.Time) error {
	throttle, err := users.Store.SignInThrottles().Get(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if left := time.UnixMilli(throttle.LockedUntil).Sub(now); left > 0 {
		return ThrottledError{RetryAfter: left}
	}
	return nil
}

// fail counts a failed attempt against name, locks it once the count
// calls for it, and returns ErrBadCredentials. A name no user could have
// is not kept: the name rule is no secret, and a stranger's arbitrary
// strings would otherwise each take a row.
func (users *Users) fail(ctx context.Context, name string, now time.Time) error {
	if !nameRe.MatchString(name) {
		return ErrBadCredentials
	}
	failures, err := users.Store.SignInThrottles().Fail(ctx, name, now.UnixMilli())
	if err != nil {
		return err
	}
	if lock := lockFor(failures); lock > 0 {
		if err := users.Store.SignInThrottles().Lock(ctx, name, now.Add(lock).UnixMilli()); err != nil {
			return err
		}
	}
	return ErrBadCredentials
}

// ChangePassword sets user's password to next. Once no change is due,
// current must be the password it replaces, and a wrong one counts against
// the name as a failed sign-in does, so a session cannot be used to guess
// the password without limit. While one is due, the session asking has
// just proved the one-time password, so current is not asked for again,
// and next may not be that same password. Every session the user holds
// but keepSession ends.
func (users *Users) ChangePassword(ctx context.Context, user *store.User, current, next, keepSession string) error {
	if !user.MustChangePassword {
		now := users.now()
		if err := users.locked(ctx, user.Name, now); err != nil {
			return err
		}
		if !CheckPassword(user.PasswordHash, current) {
			return users.fail(ctx, user.Name, now)
		}
	}
	if err := ValidPassword(next); err != nil {
		return err
	}
	if user.MustChangePassword && CheckPassword(user.PasswordHash, next) {
		return ErrPasswordReused
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := users.Store.Users().SetPassword(ctx, user.ID, hash, false); err != nil {
		return err
	}
	user.PasswordHash, user.MustChangePassword = hash, false
	if err := users.Store.SignInThrottles().Clear(ctx, user.Name); err != nil {
		return err
	}
	return users.Store.UserSessions().DeleteForUser(ctx, user.ID, sha256Hex(keepSession))
}

// StartSession opens a session for userID and returns the ID the cookie
// carries.
func (users *Users) StartSession(ctx context.Context, userID string) (string, error) {
	return users.startSession(ctx, &store.UserSession{UserID: userID})
}

// StartTokenSession opens a session for the operator token, which belongs
// to no user and ends when the token changes, and returns the ID the
// cookie carries.
func (users *Users) StartTokenSession(ctx context.Context, operatorToken string) (string, error) {
	return users.startSession(ctx, &store.UserSession{TokenHash: sha256Hex(operatorToken)})
}

func (users *Users) startSession(ctx context.Context, session *store.UserSession) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	now := users.now().UnixMilli()
	session.IDHash, session.CreatedAt, session.LastSeenAt = sha256Hex(id), now, now
	return id, users.Store.UserSessions().Create(ctx, session)
}

// Session finds the session a cookie names and its user, nil for a token
// session. One unused past the idle limit, or older than the absolute
// limit, or a token session opened with a token other than operatorToken,
// ends here and is ErrSessionNotFound like one that never existed.
func (users *Users) Session(ctx context.Context, id, operatorToken string) (*store.UserSession, *store.User, error) {
	if id == "" {
		return nil, nil, ErrSessionNotFound
	}
	session, err := users.Store.UserSessions().Get(ctx, sha256Hex(id))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	now := users.now()
	tokenChanged := session.UserID == "" && (session.TokenHash == "" ||
		subtle.ConstantTimeCompare([]byte(session.TokenHash), []byte(sha256Hex(operatorToken))) != 1)
	if tokenChanged || now.Sub(time.UnixMilli(session.LastSeenAt)) > SessionIdle ||
		now.Sub(time.UnixMilli(session.CreatedAt)) > SessionAbsolute {
		_ = users.Store.UserSessions().Delete(ctx, session.IDHash)
		return nil, nil, ErrSessionNotFound
	}
	if now.Sub(time.UnixMilli(session.LastSeenAt)) >= touchEvery {
		if err := users.Store.UserSessions().Touch(ctx, session.IDHash, now.UnixMilli()); err != nil {
			return nil, nil, err
		}
	}
	if session.UserID == "" {
		return session, nil, nil
	}
	user, err := users.Store.Users().Get(ctx, session.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrSessionNotFound
	}
	return session, user, err
}

// EndSession ends the session a cookie names; one already gone is no error.
func (users *Users) EndSession(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return users.Store.UserSessions().Delete(ctx, sha256Hex(id))
}

// sha256Hex is how a session ID, and the token a token session was opened
// with, are kept: "" stays "".
func sha256Hex(secret string) string {
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Sweep forgets what has run out, which nothing else would remove: the
// sessions past either limit whose cookie never came back, and the
// throttle counts of names with no failure for throttleMemory.
func (users *Users) Sweep(ctx context.Context) error {
	now := users.now()
	if _, err := users.Store.UserSessions().DeleteExpired(ctx,
		now.Add(-SessionIdle).UnixMilli(), now.Add(-SessionAbsolute).UnixMilli()); err != nil {
		return err
	}
	_, err := users.Store.SignInThrottles().DeleteBefore(ctx, now.Add(-throttleMemory).UnixMilli())
	return err
}

func newID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
