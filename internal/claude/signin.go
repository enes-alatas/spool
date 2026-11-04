package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// SignInArgs is the CLI's own login to a claude.ai account (ADR-0056). Run
// in a config directory of its own, it prints a link to open, reads the
// code the page shows from stdin, and writes the sign-in into that
// directory.
func SignInArgs() []string { return []string{"auth", "login", "--claudeai"} }

// SignInLink reads the CLI's output until the link the operator opens, and
// returns it. The CLI prints it after "visit:", and it is the one https
// URL on the line. When the hub's env names a terminal the CLI takes for
// one that draws links (iTerm2's or Ghostty's TERM_PROGRAM), it wraps the
// URL in an OSC 8 hyperlink and colors it, so the line is split at the
// escape sequences' ESC and BEL bytes as well as at spaces (#1304).
func SignInLink(output *bufio.Reader) (string, error) {
	for {
		line, err := output.ReadString('\n')
		for _, field := range strings.FieldsFunc(line, linkBreak) {
			start := strings.Index(field, "https://")
			if start >= 0 && strings.Contains(field[start:], "oauth/authorize") {
				return field[start:], nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("claude auth login printed no link to sign in at")
			}
			return "", err
		}
	}
}

// linkBreak ends a field of the CLI's output: a space, or the ESC and BEL
// that open and close a terminal escape sequence.
func linkBreak(char rune) bool { return unicode.IsSpace(char) || char == '\x1b' || char == '\a' }

// SignIn is what a finished login wrote into its config directory.
type SignIn struct {
	AccessToken  string
	RefreshToken string
	// ExpiresAt is when the access token expires, in Unix ms, 0 when the
	// CLI didn't say.
	ExpiresAt int64
	Scopes    []string
	OrgUUID   string
	// AccountUUID is the claude.ai account the sign-in is of, "" when the
	// CLI didn't write one.
	AccountUUID string
	// Email is the address the account signed in with, "" when the CLI
	// didn't write one.
	Email string
}

// ReadSignIn reads the sign-in a finished login left in configDir: the
// tokens from .credentials.json, and the organization and the address from
// .claude.json.
func ReadSignIn(configDir string) (SignIn, error) {
	var credentials struct {
		ClaudeAiOauth *struct {
			AccessToken  string   `json:"accessToken"`
			RefreshToken string   `json:"refreshToken"`
			ExpiresAt    int64    `json:"expiresAt"`
			Scopes       []string `json:"scopes"`
		} `json:"claudeAiOauth"`
	}
	if err := readJSON(filepath.Join(configDir, ".credentials.json"), &credentials); err != nil {
		return SignIn{}, err
	}
	oauth := credentials.ClaudeAiOauth
	if oauth == nil || oauth.AccessToken == "" {
		return SignIn{}, errors.New("claude auth login wrote no claude.ai access token")
	}
	var config struct {
		OAuthAccount *struct {
			OrganizationUUID string `json:"organizationUuid"`
			AccountUUID      string `json:"accountUuid"`
			EmailAddress     string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := readJSON(filepath.Join(configDir, ".claude.json"), &config); err != nil {
		return SignIn{}, err
	}
	if config.OAuthAccount == nil || config.OAuthAccount.OrganizationUUID == "" {
		return SignIn{}, errors.New("claude auth login wrote no organization for the account")
	}
	return SignIn{
		AccessToken:  oauth.AccessToken,
		RefreshToken: oauth.RefreshToken,
		ExpiresAt:    oauth.ExpiresAt,
		Scopes:       oauth.Scopes,
		OrgUUID:      config.OAuthAccount.OrganizationUUID,
		AccountUUID:  config.OAuthAccount.AccountUUID,
		Email:        config.OAuthAccount.EmailAddress,
	}, nil
}

// readJSON decodes the file at path into out. The error names the file,
// never its contents: the files hold the account's tokens.
func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("claude auth login: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("claude auth login: %s is not JSON", filepath.Base(path))
	}
	return nil
}

// RefreshArgs is the CLI's own status read, which also refreshes a
// claude.ai sign-in whose access token is within minutes of its expiry,
// spending no turn (ADR-0058). A refresh revokes the access token it
// replaces.
func RefreshArgs() []string { return []string{"auth", "status"} }

// SignOutArgs is the CLI's own logout. On a claude.ai sign-in it revokes
// the refresh token at the source and deletes the credentials, and exits
// 0 whether or not the revoke was accepted (ADR-0058).
func SignOutArgs() []string { return []string{"auth", "logout"} }

// WriteSignIn writes signIn into configDir as the CLI's login left it, for
// RefreshArgs to read. With expired set, the access token is written as
// already expired, so the CLI refreshes it now rather than in its last
// minutes.
func WriteSignIn(configDir string, signIn SignIn, expired bool) error {
	expiresAt := signIn.ExpiresAt
	if expired {
		// a minute past, as the refresh was probed (#928)
		expiresAt = time.Now().Add(-time.Minute).UnixMilli()
	}
	credentials, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken":  signIn.AccessToken,
		"refreshToken": signIn.RefreshToken,
		"expiresAt":    expiresAt,
		"scopes":       signIn.Scopes,
	}})
	if err != nil {
		return err
	}
	config, err := json.Marshal(map[string]any{"oauthAccount": map[string]any{"organizationUuid": signIn.OrgUUID}})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), credentials, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, ".claude.json"), config, 0o600)
}
