// Package githubapp mints GitHub App installation tokens for the hub's
// github-app connections (ADR-0043, #887). The hub holds the app's private
// key; a loop that holds the connection is handed an installation token,
// which lives an hour at most, and never the key.
package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/redact"
	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/store"
)

// DefaultAPI is GitHub's REST API, where installation tokens are minted.
const DefaultAPI = "https://api.github.com"

const (
	// MinLife is the least a token is handed out with: a cached one closer
	// to its expiry is minted afresh, so a loop's git or gh call never
	// starts on a token about to die.
	MinLife = 15 * time.Minute
	// callTimeout bounds one call to GitHub.
	callTimeout = 20 * time.Second
	// maxAnswer bounds what is read of GitHub's answer.
	maxAnswer = 64 << 10
	// errorSnippet bounds how much of a refusal is quoted in an error.
	errorSnippet = 300
)

// ParseKey reads a GitHub App's private key: an RSA key in PEM, PKCS#1 as
// GitHub hands it out, or PKCS#8.
func ParseKey(text string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(text)))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not an RSA private key in PKCS#1 or PKCS#8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA private key: GitHub App keys are RSA")
	}
	return key, nil
}

// Token is an installation token and when GitHub said it expires.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Minter mints and caches installation tokens, one per connection. It is
// safe for concurrent use: fetches of one connection share one mint.
type Minter struct {
	// API is GitHub's REST API base, DefaultAPI when empty. Tier 2 points
	// it at a fake GitHub.
	API string
	// Minted is called with each new token before it is handed out, so
	// the redactor covers it before any loop can print it. A token it
	// fails for is not handed out.
	Minted func(ctx context.Context) error

	mu      sync.Mutex
	entries map[string]*entry
	// minted is every token minted while the hub runs, expired ones too:
	// a transcript can still echo a dead token.
	minted []string
}

// entry is one connection's cached token. Its own lock is held across a
// mint, so concurrent fetches wait for one mint rather than making several.
type entry struct {
	mu sync.Mutex
	// mintedFor is the SHA-256 of the key and the installation the token
	// was minted with: a new key, or a connection made again under the same
	// name, makes the cached token one the connection no longer vouches for.
	mintedFor [32]byte
	token     Token
}

// Token returns an installation token for a github-app connection with at
// least MinLife left, minting one when the cached token has less or was
// minted under another key or installation.
func (minter *Minter) Token(ctx context.Context, connection *store.Connection) (Token, error) {
	key, err := ParseKey(connection.Secret)
	if err != nil {
		return Token{}, fmt.Errorf("the connection's private key: %w", err)
	}
	mintedFor := sha256.Sum256([]byte(connection.Config.AppID + "\x00" + strconv.FormatInt(connection.Config.InstallationID, 10) + "\x00" + connection.Secret))
	cached := minter.entry(connection.Name)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.mintedFor == mintedFor && time.Until(cached.token.ExpiresAt) >= MinLife {
		return cached.token, nil
	}
	token, err := minter.mint(ctx, connection.Config, key)
	if err != nil {
		return Token{}, err
	}
	minter.mu.Lock()
	minter.minted = append(minter.minted, token.Value)
	minter.mu.Unlock()
	if minter.Minted != nil {
		if err := minter.Minted(ctx); err != nil {
			return Token{}, fmt.Errorf("teaching the redactor a new token: %w", err)
		}
	}
	cached.mintedFor, cached.token = mintedFor, token
	return token, nil
}

// Revoke ends the connection's cached token at GitHub, so the last one a
// loop was handed dies now rather than within the hour, and forgets it. A
// refusal is returned for the caller to log: the token expires anyway.
func (minter *Minter) Revoke(ctx context.Context, name string) error {
	minter.mu.Lock()
	cached, ok := minter.entries[name]
	delete(minter.entries, name)
	minter.mu.Unlock()
	if !ok {
		return nil
	}
	cached.mu.Lock()
	token := cached.token
	cached.mu.Unlock()
	return minter.end(ctx, token)
}

// RevokeToken ends one token at GitHub, and uncaches it if it is still the
// connection's cached token. The token route ends a token this way when the
// connection changed while it was minted: the change's own revoke may have
// run before the token was cached.
func (minter *Minter) RevokeToken(ctx context.Context, name string, token Token) error {
	minter.mu.Lock()
	cached, ok := minter.entries[name]
	minter.mu.Unlock()
	if ok {
		cached.mu.Lock()
		if cached.token.Value == token.Value {
			cached.mintedFor, cached.token = [32]byte{}, Token{}
		}
		cached.mu.Unlock()
	}
	return minter.end(ctx, token)
}

// end revokes a token at GitHub, unless it is none or has expired.
func (minter *Minter) end(ctx context.Context, token Token) error {
	if token.Value == "" || time.Now().After(token.ExpiresAt) {
		return nil
	}
	answer, status, err := minter.call(ctx, http.MethodDelete, "/installation/token", "token "+token.Value)
	if err != nil {
		return fmt.Errorf("revoking the installation token: %w", err)
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("revoking the installation token: GitHub answered %d: %s", status, refusal(answer))
	}
	return nil
}

// Secrets is every token minted while the hub runs, for the redactor.
func (minter *Minter) Secrets(context.Context) ([]redact.Secret, error) {
	minter.mu.Lock()
	defer minter.mu.Unlock()
	out := make([]redact.Secret, 0, len(minter.minted))
	for _, value := range minter.minted {
		out = append(out, redact.Secret{Name: "github_app_token", Value: value})
	}
	return out, nil
}

func (minter *Minter) entry(name string) *entry {
	minter.mu.Lock()
	defer minter.mu.Unlock()
	if minter.entries == nil {
		minter.entries = map[string]*entry{}
	}
	cached, ok := minter.entries[name]
	if !ok {
		cached = &entry{}
		minter.entries[name] = cached
	}
	return cached
}

// mint asks GitHub for a new installation token, signed in as the app.
func (minter *Minter) mint(ctx context.Context, config store.ConnectionConfig, key *rsa.PrivateKey) (Token, error) {
	jwt, err := appJWT(config.AppID, key, time.Now())
	if err != nil {
		return Token{}, err
	}
	path := "/app/installations/" + strconv.FormatInt(config.InstallationID, 10) + "/access_tokens"
	answer, status, err := minter.call(ctx, http.MethodPost, path, "Bearer "+jwt)
	if err != nil {
		return Token{}, fmt.Errorf("minting an installation token: %w", err)
	}
	if status != http.StatusCreated {
		return Token{}, fmt.Errorf("minting an installation token: GitHub answered %d: %s", status, refusal(answer))
	}
	var minted struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(answer, &minted); err != nil || minted.Token == "" || minted.ExpiresAt.IsZero() {
		return Token{}, errors.New("minting an installation token: GitHub's answer holds no token and expiry")
	}
	return Token{Value: minted.Token, ExpiresAt: minted.ExpiresAt}, nil
}

// call makes one request to GitHub's API and reads its answer.
func (minter *Minter) call(ctx context.Context, method, path, authorization string) ([]byte, int, error) {
	base := minter.API
	if base == "" {
		base = DefaultAPI
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "spool")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	switch {
	case err != nil:
		return nil, 0, fmt.Errorf("reading GitHub's answer: %w", err)
	case len(answer) > maxAnswer:
		return nil, 0, fmt.Errorf("GitHub's answer ran over %d bytes", maxAnswer)
	}
	return answer, resp.StatusCode, nil
}

// client follows no redirect: its calls carry the app's JWT or a token,
// and GitHub's API has no reason to send them elsewhere.
var client = &http.Client{
	Timeout:       callTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// appJWT is the app's own credential: a JWT signed RS256 with its private
// key, issued a minute in the past against clock drift and expiring inside
// GitHub's ten-minute limit.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the app's JWT: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// refusal is the readable part of GitHub's refusal: its message when the
// answer has one, or the start of the answer, as plain text.
func refusal(answer []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	text := string(answer)
	if json.Unmarshal(answer, &body) == nil && body.Message != "" {
		text = body.Message
	}
	if len(text) > errorSnippet {
		text = text[:errorSnippet] + "…"
	}
	return roomtext.Plain(text)
}
