package claude

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The link is the authorize URL the CLI prints after "visit:", found
// whatever comes before it; output with none is an error.
func TestSignInLink(t *testing.T) {
	output := "Opening browser to sign in…\nIf the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=x\nPaste code here if prompted > "
	link, err := SignInLink(bufio.NewReader(strings.NewReader(output)))
	if err != nil || link != "https://claude.com/cai/oauth/authorize?code=true&state=x" {
		t.Fatalf("SignInLink = %q, %v", link, err)
	}
	if _, err := SignInLink(bufio.NewReader(strings.NewReader("Login failed\n"))); err == nil {
		t.Fatal("output with no link must be an error")
	}
}

// A finished login's tokens and organization are read from the two files
// the CLI writes; one missing either is an error that quotes neither file.
func TestReadSignIn(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".credentials.json", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-fixture","refreshToken":"sk-ant-ort01-fixture","expiresAt":1792228778627,"scopes":["user:inference"]}}`)
	if _, err := ReadSignIn(dir); err == nil {
		t.Fatal("a login with no .claude.json must be an error")
	}
	write(".claude.json", `{"oauthAccount":{"organizationUuid":"00000000-0000-4000-8000-000000000001"}}`)
	signIn, err := ReadSignIn(dir)
	if err != nil || signIn.AccessToken != "sk-ant-oat01-fixture" || signIn.RefreshToken != "sk-ant-ort01-fixture" ||
		signIn.ExpiresAt != 1792228778627 || signIn.OrgUUID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("ReadSignIn = %+v, %v", signIn, err)
	}
	if signIn.Email != "" {
		t.Fatalf("with no address written, ReadSignIn's email = %q, want none", signIn.Email)
	}
	write(".claude.json", `{"oauthAccount":{"organizationUuid":"00000000-0000-4000-8000-000000000001","emailAddress":"fixture@example.test"}}`)
	if signIn, err := ReadSignIn(dir); err != nil || signIn.Email != "fixture@example.test" {
		t.Fatalf("ReadSignIn's email = %q, %v, want the address written", signIn.Email, err)
	}
	write(".credentials.json", `{"claudeAiOauth":{"accessToken":""}}`)
	if _, err := ReadSignIn(dir); err == nil {
		t.Fatal("a login with no access token must be an error")
	}
	write(".credentials.json", `not json sk-ant-oat01-fixture`)
	if _, err := ReadSignIn(dir); err == nil || strings.Contains(err.Error(), "fixture") {
		t.Fatalf("an unreadable file: %v, want an error that doesn't quote it", err)
	}
}
