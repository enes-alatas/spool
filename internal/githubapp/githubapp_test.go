package githubapp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func pkcs1(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// A key is an RSA key in PEM, PKCS#1 as GitHub hands it out or PKCS#8, and
// nothing else.
func TestParseKeyTakesAnRSAKeyInPEM(t *testing.T) {
	key := testKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec8, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name, text string
		ok         bool
	}{
		{"PKCS#1", pkcs1(key), true},
		{"PKCS#8", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})), true},
		{"PKCS#1 with blank lines around it", "\n\n" + pkcs1(key) + "\n", true},
		{"an EC key", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ec8})), false},
		{"not PEM", "ghp_not-a-key", false},
		{"a PEM block of junk", string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("junk")})), false},
	} {
		if _, err := ParseKey(row.text); (err == nil) != row.ok {
			t.Errorf("%s: err %v, want ok %v", row.name, err, row.ok)
		}
	}
}

// fakeGitHub mints numbered tokens that live for life, and records the
// revocations it is asked for.
type fakeGitHub struct {
	t       *testing.T
	key     atomic.Pointer[rsa.PublicKey]
	life    atomic.Int64 // a time.Duration
	mints   atomic.Int32
	refuse  atomic.Bool
	mu      sync.Mutex
	revoked []string
}

func newFake(t *testing.T, key *rsa.PrivateKey) *fakeGitHub {
	fake := &fakeGitHub{t: t}
	fake.key.Store(&key.PublicKey)
	fake.life.Store(int64(time.Hour))
	return fake
}

func (fake *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens":
		if fake.refuse.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"A JSON web token could not be decoded"}`))
			return
		}
		fake.checkJWT(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		minted := fake.mints.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      fmt.Sprintf("ghs_synthetic%04d", minted),
			"expires_at": time.Now().Add(time.Duration(fake.life.Load())).UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodDelete && r.URL.Path == "/installation/token":
		fake.mu.Lock()
		fake.revoked = append(fake.revoked, strings.TrimPrefix(r.Header.Get("Authorization"), "token "))
		fake.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// checkJWT verifies the app's JWT as GitHub would: signed RS256 by the
// app's key, issued by the app, and within the ten minutes GitHub allows.
func (fake *fakeGitHub) checkJWT(jwt string) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		fake.t.Errorf("the JWT has %d parts, want 3", len(parts))
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		fake.t.Errorf("the JWT's signature: %v", err)
		return
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(fake.key.Load(), crypto.SHA256, digest[:], signature); err != nil {
		fake.t.Errorf("the JWT isn't signed by the app's key: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		fake.t.Errorf("the JWT's claims: %v", err)
	}
	if claims.Iss != "1234" || claims.Exp-claims.Iat > 600 || claims.Iat > time.Now().Unix() {
		fake.t.Errorf("the JWT's claims: %+v; want issued by 1234, in the past, for 10 minutes at most", claims)
	}
}

func connectionFor(key *rsa.PrivateKey) *store.Connection {
	return &store.Connection{Name: "gh-app", Kind: store.ConnectionGitHubApp, Secret: pkcs1(key),
		Config: store.ConnectionConfig{AppID: "1234", InstallationID: 42}}
}

// A token is minted once and served from the cache while it has MinLife
// left, by one mint however many fetch at once; a token with less left, or
// a new key, mints again. Each new token reaches the redactor before it is
// handed out.
func TestTheMinterCachesATokenWhileItHasLifeLeft(t *testing.T) {
	key := testKey(t)
	fake := newFake(t, key)
	server := httptest.NewServer(fake)
	defer server.Close()
	var told atomic.Int32
	minter := &Minter{API: server.URL, Minted: func(context.Context) error { told.Add(1); return nil }}
	connection := connectionFor(key)

	var wait sync.WaitGroup
	tokens := make([]Token, 8)
	for i := range tokens {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var err error
			if tokens[i], err = minter.Token(context.Background(), connection); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	for _, token := range tokens {
		if token.Value != "ghs_synthetic0001" {
			t.Fatalf("eight fetches at once got %+v; want one mint shared", tokens)
		}
	}
	if fake.mints.Load() != 1 || told.Load() != 1 {
		t.Fatalf("%d mints and %d redactor calls, want 1 each", fake.mints.Load(), told.Load())
	}
	if secrets, _ := minter.Secrets(context.Background()); len(secrets) != 1 || secrets[0].Value != "ghs_synthetic0001" {
		t.Fatalf("the redactor's tokens: %+v", secrets)
	}

	fake.life.Store(int64(MinLife - time.Minute))
	_ = minter.Revoke(context.Background(), connection.Name)
	short, _ := minter.Token(context.Background(), connection)
	again, _ := minter.Token(context.Background(), connection)
	if short.Value == again.Value {
		t.Errorf("a token with under MinLife left was served again: %s", again.Value)
	}

	fake.life.Store(int64(time.Hour))
	first, _ := minter.Token(context.Background(), connection)
	newKey := testKey(t)
	fake.key.Store(&newKey.PublicKey)
	rotated, err := minter.Token(context.Background(), connectionFor(newKey))
	if err != nil || rotated.Value == first.Value {
		t.Errorf("a new key was served the old key's token: %+v, %v", rotated, err)
	}
	if secrets, _ := minter.Secrets(context.Background()); len(secrets) != int(fake.mints.Load()) {
		t.Errorf("the redactor knows %d tokens of %d minted", len(secrets), fake.mints.Load())
	}
}

// GitHub's refusal comes back in the error, its message and status, and
// caches nothing.
func TestAMintGitHubRefusesIsAnError(t *testing.T) {
	key := testKey(t)
	fake := newFake(t, key)
	fake.refuse.Store(true)
	server := httptest.NewServer(fake)
	defer server.Close()
	minter := &Minter{API: server.URL}
	_, err := minter.Token(context.Background(), connectionFor(key))
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "could not be decoded") {
		t.Fatalf("a refused mint: %v", err)
	}
	if secrets, _ := minter.Secrets(context.Background()); len(secrets) != 0 {
		t.Errorf("a refused mint left tokens for the redactor: %+v", secrets)
	}
}

// Revoke ends the cached token at GitHub and forgets it, so the next fetch
// mints afresh; a connection with nothing cached asks GitHub nothing.
func TestRevokeEndsTheCachedTokenAtGitHub(t *testing.T) {
	key := testKey(t)
	fake := newFake(t, key)
	server := httptest.NewServer(fake)
	defer server.Close()
	minter := &Minter{API: server.URL}
	connection := connectionFor(key)
	if err := minter.Revoke(context.Background(), connection.Name); err != nil {
		t.Fatal(err)
	}
	token, err := minter.Token(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	if err := minter.Revoke(context.Background(), connection.Name); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	revoked := fake.revoked
	fake.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != token.Value {
		t.Fatalf("GitHub was asked to revoke %v, want only %s", revoked, token.Value)
	}
	if next, _ := minter.Token(context.Background(), connection); next.Value == token.Value {
		t.Errorf("a revoked token was served again: %s", next.Value)
	}
}

// RevokeToken ends the token it is given, and uncaches it only while it is
// the connection's cached token: another fetch's newer token stays served.
func TestRevokeTokenEndsOneToken(t *testing.T) {
	key := testKey(t)
	fake := newFake(t, key)
	server := httptest.NewServer(fake)
	defer server.Close()
	minter := &Minter{API: server.URL}
	connection := connectionFor(key)
	first, err := minter.Token(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	if err := minter.RevokeToken(context.Background(), connection.Name, first); err != nil {
		t.Fatal(err)
	}
	second, _ := minter.Token(context.Background(), connection)
	if second.Value == first.Value {
		t.Fatalf("a revoked token was served again: %s", second.Value)
	}
	if err := minter.RevokeToken(context.Background(), connection.Name, first); err != nil {
		t.Fatal(err)
	}
	if again, _ := minter.Token(context.Background(), connection); again.Value != second.Value {
		t.Errorf("revoking an older token uncached the newer one: served %s, want %s", again.Value, second.Value)
	}
	fake.mu.Lock()
	revoked := fake.revoked
	fake.mu.Unlock()
	if len(revoked) != 2 || revoked[0] != first.Value || revoked[1] != first.Value {
		t.Errorf("GitHub was asked to revoke %v, want %s twice", revoked, first.Value)
	}
}
