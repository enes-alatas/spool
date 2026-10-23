//go:build integration

package itest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/githubapp"
)

// fakeGitHubApps stands in for GitHub's API: it mints an installation
// token for an app JWT signed by any key it was given, each living
// githubapp.MinLife plus a few seconds, and records the tokens it is asked
// to revoke.
type fakeGitHubApps struct {
	t       *testing.T
	mu      sync.Mutex
	keys    []*rsa.PublicKey
	tokens  []string
	revoked []string
	refused int // mints refused for a JWT none of keys signed
	// beforeMint, when set, runs once ahead of the next mint, outside mu.
	beforeMint func()
}

// fakeTokenSlack is how long a fake token lives past MinLife: a token is
// served from the hub's cache until then, and minted afresh after. It
// must outlast the fetches a test expects served from the cache, a wake
// each, after expires_at loses up to a second to GitHub's whole-second
// format: at 3s, two fetches across an attach's restart outran it.
const fakeTokenSlack = 8 * time.Second

func startFakeGitHubApps(t *testing.T) (*fakeGitHubApps, string) {
	t.Helper()
	fake := &fakeGitHubApps{t: t}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server.URL
}

// newAppKey is a fresh app key, in PEM as GitHub hands it out, which the
// fake accepts JWTs from.
func (fake *fakeGitHubApps) newAppKey() string {
	fake.t.Helper()
	key, text := fake.generateKey()
	fake.mu.Lock()
	fake.keys = append(fake.keys, &key.PublicKey)
	fake.mu.Unlock()
	return text
}

// strangerAppKey is a fresh app key the fake refuses JWTs from, as GitHub
// refuses a deleted one.
func (fake *fakeGitHubApps) strangerAppKey() string {
	fake.t.Helper()
	_, text := fake.generateKey()
	return text
}

func (fake *fakeGitHubApps) generateKey() (*rsa.PrivateKey, string) {
	fake.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fake.t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// refusals is how many mints the fake has refused.
func (fake *fakeGitHubApps) refusals() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.refused
}

func (fake *fakeGitHubApps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	if hook := fake.beforeMint; hook != nil && r.Method == http.MethodPost {
		fake.beforeMint = nil
		fake.mu.Unlock()
		hook()
		fake.mu.Lock()
	}
	defer fake.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens":
		if !fake.signed(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			fake.refused++
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"A JSON web token could not be decoded"}`))
			return
		}
		token := fmt.Sprintf("ghs_fixtureAPPTOKEN%04d", len(fake.tokens)+1)
		fake.tokens = append(fake.tokens, token)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      token,
			"expires_at": time.Now().Add(githubapp.MinLife + fakeTokenSlack).UTC().Format(time.RFC3339),
		})
	case r.Method == http.MethodDelete && r.URL.Path == "/installation/token":
		fake.revoked = append(fake.revoked, strings.TrimPrefix(r.Header.Get("Authorization"), "token "))
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// signed reports whether a JWT is signed RS256 by one of the fake's keys.
// The caller holds mu.
func (fake *fakeGitHubApps) signed(jwt string) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	for _, key := range fake.keys {
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) == nil {
			return true
		}
	}
	return false
}

// minted is every token the fake has minted, and revokedTokens every one
// it was asked to revoke.
func (fake *fakeGitHubApps) minted() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.tokens...)
}

func (fake *fakeGitHubApps) revokedTokens() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.revoked...)
}

// A loop that holds a github-app connection fetches an installation token
// from the hub's token route (#887): one the hub minted with the app's key,
// served from its cache while it has MinLife left and minted afresh after,
// and redacted from the loop's stored reply. A new key ends no session and
// mints under the new key, ending the old key's token at GitHub. A revoke
// ends the last token there too and refuses the next fetch. The key itself is in no response and no log line.
func TestALoopFetchesItsGitHubAppTokenFromTheHub(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)
	key := fake.newAppKey()
	var created map[string]any
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": key,
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, &created)
	if raw, _ := json.Marshal(created); strings.Contains(string(raw), "PRIVATE KEY") || created["has_secret"] != true {
		t.Fatalf("the created connection: %s; want has_secret and no key", raw)
	}

	asked := map[string]bool{}
	fetch := func() string {
		t.Helper()
		hub.scriptLoop("aster", "!github-token")
		hub.message("aster", "go")
		done := hub.waitTurn("aster", 30*time.Second, func(done turn) bool {
			return !asked[done.ID] && strings.HasPrefix(done.ResultText, "github token:")
		})
		asked[done.ID] = true
		return strings.TrimPrefix(done.ResultText, "github token: ")
	}
	tokenSum := func(reply string) string {
		t.Helper()
		sum, rest, _ := strings.Cut(reply, " ")
		if len(sum) != 12 || !strings.HasPrefix(rest, "<redacted:github_app_token>") {
			t.Fatalf("a fetch replied %q; want a token's sum and the token redacted", reply)
		}
		return sum
	}
	sumOf := func(token string) string {
		sum := sha256.Sum256([]byte(token))
		return hex.EncodeToString(sum[:])[:12]
	}

	if reply := fetch(); !strings.HasPrefix(reply, "error: 404") {
		t.Fatalf("a loop holding no app fetched %q, want 404", reply)
	}
	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)
	first := tokenSum(fetch())
	if again := tokenSum(fetch()); again != first || len(fake.minted()) != 1 {
		t.Fatalf("a fetch with the token's life left minted again: %s then %s, %d minted", first, again, len(fake.minted()))
	}
	if first != sumOf(fake.minted()[0]) {
		t.Fatalf("the loop got %s, not the token GitHub minted", first)
	}

	time.Sleep(fakeTokenSlack + time.Second) // the cached token now has under MinLife left
	second := tokenSum(fetch())
	if second == first || second != sumOf(fake.minted()[1]) {
		t.Fatalf("past MinLife the loop got %s again, want a new token", second)
	}

	session := hub.loop("aster").CurrentSessionID
	hub.mustJSON("PUT", "/api/connections/gh-app/secret", map[string]any{"value": fake.newAppKey()}, nil)
	third := tokenSum(fetch())
	if third == second || len(fake.minted()) != 3 {
		t.Fatalf("after a new key the loop got %s, want a token minted under it", third)
	}
	if revoked := fake.revokedTokens(); len(revoked) != 1 || sumOf(revoked[0]) != second {
		t.Fatalf("a new key asked GitHub to revoke %v, want the old key's token", revoked)
	}
	if now := hub.loop("aster").CurrentSessionID; now != session {
		t.Fatalf("a new app key ended the session %s; the loop never held the key", session)
	}

	hub.mustJSON("POST", "/api/connections/gh-app/revoke", nil, nil)
	if revoked := fake.revokedTokens(); len(revoked) != 2 || sumOf(revoked[1]) != third {
		t.Fatalf("revoking asked GitHub to revoke %v, want the last token handed out", revoked)
	}
	if reply := fetch(); !strings.HasPrefix(reply, "error: 404") {
		t.Fatalf("a revoked app's loop fetched %q, want 404", reply)
	}

	logged := hub.log()
	for _, token := range fake.minted() {
		if strings.Contains(logged, token) {
			t.Errorf("the hub's log holds minted token %s", token)
		}
	}
	if strings.Contains(logged, "PRIVATE KEY") {
		t.Error("the hub's log holds the app's private key")
	}
}

// A revoke that lands while a token is minted refuses that fetch, and the
// token ends at GitHub (#887): the route reads the connection again after
// the mint, so no token outlives a revoke it raced.
func TestARevokeDuringAMintEndsThatToken(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": fake.newAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	hub.scriptLoop("aster", "!github-token") // its wake turns fetch before the app is attached
	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)
	seen := map[string]bool{}
	for _, done := range hub.completed("aster") {
		seen[done.ID] = true
	}

	revokeDone := make(chan int, 1)
	fake.mu.Lock()
	fake.beforeMint = func() {
		// The revoke waits on the mint to end the cached token, so it runs
		// beside it; the mint answers once the revoke is stored.
		go func() {
			resp, _ := hub.do("POST", "/api/connections/gh-app/revoke", nil)
			revokeDone <- resp.StatusCode
		}()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var connection struct {
				RevokedAt int64 `json:"revoked_at"`
			}
			if resp, body := hub.do("GET", "/api/connections/gh-app", nil); resp.StatusCode == 200 &&
				json.Unmarshal(body, &connection) == nil && connection.RevokedAt != 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("the revoke was never stored")
	}
	fake.mu.Unlock()

	hub.message("aster", "go")
	hub.waitTurn("aster", 30*time.Second, func(done turn) bool {
		return !seen[done.ID] && strings.HasPrefix(done.ResultText, "github token:")
	})
	select {
	case status := <-revokeDone:
		if status != 200 {
			t.Fatalf("the revoke answered %d", status)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no mint ran the revoke: the fetch was served from the cache")
	}
	hub.waitState("aster", "asleep", 30*time.Second) // the revoke's rotation may wake the loop again
	for _, done := range hub.completed("aster") {
		if reply, ok := strings.CutPrefix(done.ResultText, "github token: "); ok && !seen[done.ID] && !strings.HasPrefix(reply, "error: 404") {
			t.Errorf("a fetch the revoke raced got %q, want 404", reply)
		}
	}
	minted := fake.minted()
	if len(minted) != 1 || !slices.Contains(fake.revokedTokens(), minted[0]) {
		t.Fatalf("minted %d tokens and revoked %d; want the one minted ended at GitHub", len(minted), len(fake.revokedTokens()))
	}
}

// A mint GitHub refuses is remembered briefly (#1057): a loop fetching again
// gets the same refusal, saying so, and GitHub isn't asked twice. A new key
// mints at once.
func TestARefusedGitHubAppMintIsRememberedBriefly(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": fake.strangerAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)
	hub.scriptLoop("aster", "!github-token")
	asked := map[string]bool{}
	fetch := func() string {
		t.Helper()
		hub.message("aster", "go")
		done := hub.waitTurn("aster", 30*time.Second, func(done turn) bool {
			return !asked[done.ID] && strings.HasPrefix(done.ResultText, "github token:")
		})
		asked[done.ID] = true
		return strings.TrimPrefix(done.ResultText, "github token: ")
	}

	first, again := fetch(), fetch()
	if !strings.HasPrefix(first, "error: 502") || !strings.HasPrefix(again, "error: 502") || !strings.Contains(again, "remembered") {
		t.Fatalf("two fetches with a key GitHub refuses: %q, then %q; want 502 twice, the second remembered", first, again)
	}
	if refused := fake.refusals(); refused != 1 {
		t.Fatalf("GitHub refused %d mints, want 1: the second fetch asked again", refused)
	}

	hub.mustJSON("PUT", "/api/connections/gh-app/secret", map[string]any{"value": fake.newAppKey()}, nil)
	if reply := fetch(); !strings.Contains(reply, "<redacted:github_app_token>") || len(fake.minted()) != 1 {
		t.Fatalf("after a new key the loop got %q, want a token minted at once", reply)
	}
}

// A loop holds one GitHub identity (#887): a second github-app, or an
// env-var on GH_TOKEN beside one, is refused in either order, and a cloud
// loop can't hold one, since its listener doesn't serve the token route.
func TestALoopHoldsOneGitHubIdentity(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startCloudHub(t, "cirrus", "--github-api-base", api)
	hub.createLoop("aster", nil)
	hub.createLoop("basil", nil)
	for _, name := range []string{"gh-app", "gh-app-two"} {
		hub.mustJSON("POST", "/api/connections", map[string]any{
			"name": name, "kind": "github-app", "secret": fake.newAppKey(),
			"config": map[string]any{"app_id": "1234", "installation_id": 42},
		}, nil)
	}
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-token", "kind": "env-var", "secret": "ghp_fixturePAT0000",
		"config": map[string]any{"env": "GH_TOKEN"},
	}, nil)
	conflict := func(path, want string) {
		t.Helper()
		resp, body := hub.do("PUT", path, nil)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), want) {
			t.Errorf("PUT %s = %d %s, want 409 %s", path, resp.StatusCode, body, want)
		}
	}

	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)
	conflict("/api/loops/aster/connections/gh-app-two", "connection_github_taken")
	conflict("/api/loops/aster/connections/gh-token", "connection_github_taken")
	hub.mustJSON("PUT", "/api/loops/basil/connections/gh-token", nil, nil)
	conflict("/api/loops/basil/connections/gh-app", "connection_github_taken")
	conflict("/api/loops/cirrus/connections/gh-app-two", "connection_runtime")
	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil) // attaching what is attached changes nothing
}

// An env-var on a variable the hub sets for a github-app holder's git and
// gh, PATH or git's env config among them, is refused beside a github-app
// in either order (#1060): it would win the clash and leave git or gh
// unauthenticated. One on any other variable attaches.
func TestAnEnvVarCantShadowTheGitHubHelpersEnv(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)
	hub.createLoop("basil", nil)
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": fake.newAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	for name, env := range map[string]string{"own-path": "PATH", "own-helper": "GIT_CONFIG_VALUE_1", "own-route": "SPOOL_GITHUB_TOKEN_URL", "own-params": "GIT_CONFIG_PARAMETERS", "editor": "EDITOR"} {
		hub.mustJSON("POST", "/api/connections", map[string]any{
			"name": name, "kind": "env-var", "secret": "fixture-value", "config": map[string]any{"env": env},
		}, nil)
	}
	conflict := func(path, want string) {
		t.Helper()
		resp, body := hub.do("PUT", path, nil)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "connection_github_env") || !strings.Contains(string(body), want) {
			t.Errorf("PUT %s = %d %s, want 409 connection_github_env naming %s", path, resp.StatusCode, body, want)
		}
	}

	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)
	conflict("/api/loops/aster/connections/own-path", "PATH")
	conflict("/api/loops/aster/connections/own-helper", "GIT_CONFIG_VALUE_1")
	conflict("/api/loops/aster/connections/own-route", "SPOOL_GITHUB_TOKEN_URL")
	conflict("/api/loops/aster/connections/own-params", "GIT_CONFIG_PARAMETERS")
	hub.mustJSON("PUT", "/api/loops/aster/connections/editor", nil, nil)
	hub.mustJSON("PUT", "/api/loops/basil/connections/own-path", nil, nil)
	conflict("/api/loops/basil/connections/gh-app", "PATH")
}

// A loop holding a github-app gets its token in git and gh with nothing of
// its own to run (#1054). Its git asks spool-github, the credential helper
// the hub registers through git's env config, and is answered with the
// hub's token, a new one once the cached one is under MinLife. Its gh is
// spool-github's link, first on PATH, which runs the real gh with GH_TOKEN
// set. Neither token is in a reply as itself. A loop holding no app is
// handed none of it, and the gh link with no route passes straight through.
func TestALoopsGitAndGHGetItsGitHubAppToken(t *testing.T) {
	t.Parallel()
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)
	hub.createLoop("basil", nil)
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": fake.newAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	hub.mustJSON("PUT", "/api/loops/aster/connections/gh-app", nil, nil)

	// A stand-in for the real gh, which says what it was run with: its
	// arguments and the SHA-256 of the GH_TOKEN it got.
	tools := t.TempDir()
	writeExecutable(t, filepath.Join(tools, "gh"), "#!/bin/sh\nprintf 'gh %s: ' \"$*\"; printf %s \"$GH_TOKEN\" | sha256sum | cut -c1-12\n")
	// Each turn runs the commands in this file, which the test rewrites:
	// the loop's script line is the same every turn.
	commands := filepath.Join(t.TempDir(), "turn.sh")
	asked := map[string]bool{}
	run := func(loopName, script string) string {
		t.Helper()
		if err := os.WriteFile(commands, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		hub.message(loopName, "go")
		done := hub.waitTurn(loopName, 30*time.Second, func(done turn) bool {
			return !asked[done.ID] && strings.HasPrefix(done.ResultText, "sh: ")
		})
		asked[done.ID] = true
		return strings.TrimPrefix(done.ResultText, "sh: ")
	}
	for _, name := range []string{"aster", "basil"} {
		hub.scriptLoop(name, "!sh sh "+commands)
		for _, done := range hub.completed(name) {
			asked[done.ID] = true
		}
	}
	sumOf := func(token string) string {
		sum := sha256.Sum256([]byte(token))
		return hex.EncodeToString(sum[:])[:12]
	}
	const fill = "printf 'protocol=https\\nhost=github.com\\n\\n' | GIT_TERMINAL_PROMPT=0 git credential fill"
	// the gh stand-in goes right after the link's directory, ahead of any gh
	// the host has, so the link finds it as the real gh
	const gh = `PATH="${PATH%%:*}:` + "TOOLS" + `:${PATH#*:}" gh`

	if answer := run("aster", fill); !strings.Contains(answer, "username=x-access-token") || !strings.Contains(answer, "password=<redacted:github_app_token>") {
		t.Fatalf("the loop's git was answered %q; want x-access-token and the token, redacted", answer)
	}
	first := run("aster", fill+" | sed -n 's/^password=//p' | tr -d '\\n' | sha256sum | cut -c1-12")
	if len(fake.minted()) != 1 || first != sumOf(fake.minted()[0]) {
		t.Fatalf("the loop's git got a token summing %q; want GitHub's first, %d minted", first, len(fake.minted()))
	}
	time.Sleep(fakeTokenSlack + time.Second) // the cached token now has under MinLife left
	second := run("aster", fill+" | sed -n 's/^password=//p' | tr -d '\\n' | sha256sum | cut -c1-12")
	if second == first || len(fake.minted()) != 2 || second != sumOf(fake.minted()[1]) {
		t.Fatalf("past MinLife the loop's git got %q again; want a new token", second)
	}
	if answer := run("aster", strings.ReplaceAll(gh, "TOOLS", tools)+" pr list"); answer != "gh pr list: "+second {
		t.Fatalf("the loop's gh ran as %q; want the real gh with the current token as GH_TOKEN", answer)
	}
	environment := run("aster", "env")
	if !strings.Contains(environment, "SPOOL_HUB_TOKEN=<redacted:hub_mcp_token>") || !strings.Contains(environment, "GIT_CONFIG_COUNT=2") {
		t.Fatalf("the loop's env: %s; want its hub token, redacted, and git's helper", environment)
	}

	if answer := run("basil", "env | grep -c '^SPOOL_GITHUB\\|^SPOOL_HUB\\|^GIT_CONFIG' || true"); answer != "0" {
		t.Fatalf("a loop holding no app has %s of the github env, want none", answer)
	}
	link := filepath.Join(hub.dataDir, "github-bin", "gh")
	if answer := run("basil", "GH_TOKEN=ghp_fixtureOWN PATH="+tools+":$PATH "+link+" auth status"); answer != "gh auth status: "+sumOf("ghp_fixtureOWN") {
		t.Fatalf("the gh link with no route ran as %q; want the real gh with the loop's own GH_TOKEN", answer)
	}

	for _, name := range []string{"aster", "basil"} {
		raw, _ := json.Marshal(hub.turns(name))
		for _, token := range fake.minted() {
			if strings.Contains(string(raw), token) {
				t.Errorf("%s's turns hold minted token %s", name, token)
			}
		}
	}
	for _, token := range fake.minted() {
		if strings.Contains(hub.log(), token) {
			t.Errorf("the hub's log holds minted token %s", token)
		}
	}
}

func writeExecutable(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
}
