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
)

// SignInArgs is the CLI's own login to a claude.ai account (ADR-0056). Run
// in a config directory of its own, it prints a link to open, reads the
// code the page shows from stdin, and writes the sign-in into that
// directory.
func SignInArgs() []string { return []string{"auth", "login", "--claudeai"} }

// SignInLink reads the CLI's output until the link the operator opens, and
// returns it. The CLI prints it after "visit:", and it is the one https
// URL on the line.
func SignInLink(output *bufio.Reader) (string, error) {
	for {
		line, err := output.ReadString('\n')
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "https://") && strings.Contains(field, "oauth/authorize") {
				return field, nil
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

// SignIn is what a finished login wrote into its config directory.
type SignIn struct {
	AccessToken  string
	RefreshToken string
	// ExpiresAt is when the access token expires, in Unix ms, 0 when the
	// CLI didn't say.
	ExpiresAt int64
	Scopes    []string
	OrgUUID   string
}

// ReadSignIn reads the sign-in a finished login left in configDir: the
// tokens from .credentials.json and the organization from .claude.json.
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
