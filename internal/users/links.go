package users

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// LinkCodeTTL is how long a link code links: long enough to switch to a
// chat app and send it, short enough that a code left on a screen is
// soon worth nothing. It links once (#703).
const LinkCodeTTL = 15 * time.Minute

// LinkConfirmTTL is how long a person has to reply YES once a code has
// reached them, before it links nobody (ADR-0051).
const LinkConfirmTTL = 5 * time.Minute

// A link code is linkCodePrefix and linkCodeLength characters of the
// one-time alphabet: the prefix keeps a DM that is one from being
// mistaken for anything a person says to a loop.
const (
	linkCodePrefix = "link-"
	linkCodeLength = 10
)

var linkCodeRe = regexp.MustCompile(`^` + linkCodePrefix + `[` + oneTimeAlphabet + `]{` + strconv.Itoa(linkCodeLength) + `}$`)

var (
	// ErrLinkCodeUnknown answers a code that never was, was spent, expired
	// or was minted for another surface: the four are told apart to nobody.
	ErrLinkCodeUnknown = errors.New("that link code is unknown, spent or expired")
	// ErrPersonLinked answers a code sent by a person another user has
	// already linked.
	ErrPersonLinked = errors.New("that account is linked to another user")
	// ErrNothingToConfirm answers a YES from a person no code has asked,
	// or asked more than LinkConfirmTTL ago.
	ErrNothingToConfirm = errors.New("there is no link to confirm")
	// ErrNoSuchSurface answers a code asked for a surface the hub does
	// not know.
	ErrNoSuchSurface = errors.New(`a link code is for "telegram" or "slack"`)
)

// NewLinkCode mints a link code for the user to send on surface, and keeps
// its hash, in place of any code they asked for before. It is shown to
// them once, and links nobody on another surface.
func (users *Users) NewLinkCode(ctx context.Context, userID, surface string) (code string, expiresAt time.Time, err error) {
	if surface != store.SurfaceTelegram && surface != store.SurfaceSlack {
		return "", time.Time{}, ErrNoSuchSurface
	}
	random, err := readableRandom(linkCodeLength)
	if err != nil {
		return "", time.Time{}, err
	}
	code = linkCodePrefix + random
	expiresAt = users.now().Add(LinkCodeTTL)
	if err := users.Store.UserLinks().SetCode(ctx, userID, surface, linkCodeHash(code), expiresAt.UnixMilli()); err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// LinkCodeOf reports whether a DM's text is a link code, and the code as
// it is kept. Case and the space around it are forgiven, since a phone
// capitalises what it is given.
func LinkCodeOf(text string) (string, bool) {
	code := strings.ToLower(strings.TrimSpace(text))
	return code, linkCodeRe.MatchString(code)
}

// ClaimLink spends code and asks confirmation.Person, the sender who sent
// it, to confirm a link to its user within LinkConfirmTTL. It answers with
// that user.
func (users *Users) ClaimLink(ctx context.Context, code string, confirmation store.LinkConfirmation) (*store.User, error) {
	now := users.now()
	confirmation.ExpiresAt = now.Add(LinkConfirmTTL).UnixMilli()
	err := users.Store.UserLinks().Claim(ctx, linkCodeHash(code), &confirmation, now.UnixMilli())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, ErrLinkCodeUnknown
	case errors.Is(err, store.ErrDuplicate):
		return nil, ErrPersonLinked
	case err != nil:
		return nil, err
	}
	return users.Store.Users().Get(ctx, confirmation.UserID)
}

// ConfirmLink links person to the user a code asked them to confirm, and
// makes them an allowed sender on their surface.
func (users *Users) ConfirmLink(ctx context.Context, person string) (*store.User, error) {
	link, err := users.Store.UserLinks().Confirm(ctx, person, users.now().UnixMilli())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, ErrNothingToConfirm
	case errors.Is(err, store.ErrDuplicate):
		return nil, ErrPersonLinked
	case err != nil:
		return nil, err
	}
	return users.Store.Users().Get(ctx, link.UserID)
}

// linkCodeHash is how a link code is kept. A code is about 49 random bits
// that link once within 15 minutes, not a password a person chose, so a
// fast hash is enough, as it is for a session ID.
func linkCodeHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
