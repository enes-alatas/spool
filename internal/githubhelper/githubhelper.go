// Package githubhelper is spool-github, the helper that hands a loop's git
// and gh the GitHub token of the github-app connection it holds (#1054).
// The hub keeps the app's key and mints the tokens (ADR-0043); this side
// only asks the hub's token route for one, with the loop's own hub token,
// each time git or gh needs it. A loop holding no app is not handed the
// route, and then the helper gives git nothing and gh passes straight
// through.
package githubhelper

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The env the hub hands a loop holding a github-app: the token route as its
// workstation reaches it, and the loop's own hub token to ask it with.
const (
	EnvTokenURL = "SPOOL_GITHUB_TOKEN_URL"
	EnvHubToken = "SPOOL_HUB_TOKEN"
)

// Host is the one host the helper answers git for.
const Host = "github.com"

// fetchTimeout bounds one ask of the hub, which may mint the token first.
const fetchTimeout = 30 * time.Second

// maxToken caps the hub's answer: an installation token is a few dozen
// bytes.
const maxToken = 4096

// client goes to the hub directly, past any HTTP_PROXY: the route is on
// the loop listener, which a workstation reaches as it reaches /mcp.
var client = &http.Client{
	Timeout:   fetchTimeout,
	Transport: &http.Transport{Proxy: nil},
}

// Token asks the hub's token route for a fresh token. ok is false when the
// loop was handed no route, a loop that holds no app.
func Token(getenv func(string) string) (token string, ok bool, err error) {
	url, hubToken := getenv(EnvTokenURL), getenv(EnvHubToken)
	if url == "" || hubToken == "" {
		return "", false, nil
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", true, err
	}
	req.Header.Set("Authorization", "Bearer "+hubToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("asking the hub for a GitHub token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxToken+1))
	switch {
	case err != nil:
		return "", true, fmt.Errorf("reading the hub's GitHub token: %w", err)
	case resp.StatusCode == http.StatusNotFound:
		return "", true, errors.New("the hub has no GitHub App for this loop: it was detached or revoked")
	case resp.StatusCode != http.StatusOK:
		return "", true, fmt.Errorf("the hub answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	case len(body) > maxToken:
		return "", true, errors.New("the hub's GitHub token ran over its size")
	}
	return strings.TrimSpace(string(body)), true, nil
}

// Credential is git's credential helper (gitcredentials(7)): asked to get
// a credential for https://github.com, it answers with a fresh token as the
// password of x-access-token, the name an installation token takes. Any
// other host, store and erase are answered with nothing, and git asks its
// next helper.
func Credential(operation string, stdin io.Reader, stdout io.Writer, getenv func(string) string) error {
	if operation != "get" {
		_, _ = io.Copy(io.Discard, stdin)
		return nil
	}
	asked := map[string]string{}
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			asked[key] = value
		}
	}
	if asked["protocol"] != "https" || asked["host"] != Host {
		return nil
	}
	token, ok, err := Token(getenv)
	if !ok || err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "username=x-access-token\npassword=%s\n", token)
	return err
}

// GH is the gh the helper stands in for: the real gh further along PATH
// than self, and its env, with GH_TOKEN set to a fresh token for a loop
// holding an app and left as it was for any other.
func GH(self string, environ []string, getenv func(string) string) (path string, env []string, err error) {
	path, err = realGH(self, getenv("PATH"))
	if err != nil {
		return "", nil, err
	}
	token, ok, err := Token(getenv)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return path, environ, nil
	}
	env = make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "GH_TOKEN=") {
			env = append(env, entry)
		}
	}
	return path, append(env, "GH_TOKEN="+token), nil
}

// realGH is the first gh on PATH that isn't the helper itself.
func realGH(self, pathList string) (string, error) {
	selfInfo, err := os.Stat(self)
	if err != nil {
		return "", err
	}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "gh")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 || os.SameFile(info, selfInfo) {
			continue
		}
		return candidate, nil
	}
	return "", errors.New("gh is not installed: no gh on PATH but this one")
}
