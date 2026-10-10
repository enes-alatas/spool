package hook

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Invented values in each credential shape, built at run time so the line
// that writes one is not itself a credential shape for the diff's secret
// scan. The letters are arbitrary and deliberately free of the runs and
// sequences that would make them read as synthetic: the hook refuses only
// a value that could be real.
const invented = "BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx"

var (
	telegramToken = "7196384205" + ":" + invented
	anthropicKey  = "sk-ant" + "-oat01-" + invented
	githubToken   = "ghp" + "_" + invented
	bearerHeader  = "Bearer" + " " + invented
	assigned      = "password" + "=" + invented
)

// bashIn is a Bash call made from the directory dir.
func bashIn(dir, command string) Call {
	call := bash(command)
	call.Cwd = dir
	return call
}

// TestCredentialsHandedToGhOrGitAreRefused: each shape, reaching gh or git
// the ways a loop hands a value over, and the next obvious ways after that
// (#413).
func TestCredentialsHandedToGhOrGitAreRefused(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"body.md":   "Rotated it: " + telegramToken + "\n",
		"msg.txt":   "fix: the key\n\nwas " + anthropicKey + "\n",
		"req.json":  `{"body": "` + bearerHeader + `"}`,
		"notes.txt": "plain notes\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{
		// as an argument
		"gh issue comment 12 --body 'the token is " + githubToken + "'",
		"gh pr create --title fix --body \"" + assigned + "\"",
		"git commit -m 'fix: rotate " + telegramToken + "'",
		"git remote set-url origin https://x-access-token:" + githubToken + "@github.com/o/r.git",
		"git -c http.extraHeader='Authorization: " + bearerHeader + "' fetch",
		"git config --global credential.token " + githubToken,
		"gh api -H 'Authorization: " + bearerHeader + "' /user",
		"gh api repos/o/r/issues -F body='" + assigned + "'",
		"sudo git commit -m '" + anthropicKey + "'",
		"cd sub && git commit -m 'key " + anthropicKey + "'",
		"/usr/bin/gh issue comment 3 -b " + githubToken,
		"GH_TOKEN=" + githubToken + " gh pr list",
		"sh -c 'gh issue comment 1 -b " + githubToken + "'",
		// the shapes fold, as the scan's do
		"gh issue comment 1 -b " + strings.ToUpper(githubToken),
		"gh issue comment 1 -b 'API_KEY: " + invented + "'",
		// on standard input
		"echo " + githubToken + " | gh auth login --with-token",
		"gh auth login --with-token <<< " + githubToken,
		"gh pr create --body-file - <<'EOF'\nRotated: " + telegramToken + "\nEOF",
		"git commit -F - <<'EOF'\nfix: key\n\n" + anthropicKey + "\nEOF",
		"gh issue comment 4 --body \"$(cat <<'EOF'\n" + assigned + "\nEOF\n)\"",
		// in a body written by the same call
		"cat > new.md <<'EOF'\n" + githubToken + "\nEOF\ngh pr create --body-file new.md",
		// in a body file written before the call
		"gh pr create --title fix --body-file body.md",
		"gh pr create --title fix -F body.md",
		"gh pr comment 9 --body-file=body.md",
		"gh pr create -F " + filepath.Join(dir, "body.md"),
		"git commit -F msg.txt",
		"git commit --file msg.txt",
		"git commit --file=msg.txt",
		"gh api repos/o/r/issues --input req.json",
		"gh api repos/o/r/pulls/9/comments -F body=@body.md",
		"gh api repos/o/r/pulls/9/comments -F path=a.go --field body=@body.md",
		"gh api repos/o/r/pulls/9/comments --field=body=@body.md",
		"gh api repos/o/r/pulls/9/comments -Fbody=@body.md",
		"gh release create v1 --notes-file body.md",
		"gh release create v1 --notes-file=body.md",
		"gh gist create body.md",
		"gh gist create -d notes notes.txt body.md",
		"gh gist edit 5b0e0062eb8e9654adad7bb1d81cc75f --add body.md",
	} {
		reason := Check(bashIn(dir, command))
		if reason == "" {
			t.Errorf("let through: %s", command)
			continue
		}
		if strings.Contains(reason, invented[:8]) {
			t.Errorf("the reason quotes the value: %s", reason)
		}
	}
}

// TestCallsWithoutACredentialRun: redacted quotes, fixtures, a body file
// with nothing in it, and a credential that never reaches gh or git.
func TestCallsWithoutACredentialRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("Rotated ghp_BQFgtZmK… today.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file holding a value, which none of these calls publishes.
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte(githubToken), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"gh issue comment 12 --body 'the leaked token was ghp_BQFgtZmK…, rotated'",
		"gh issue comment 12 --body 'set GH_TOKEN and run gh auth status'",
		"gh pr create --body-file notes.txt",
		"gh pr create --body-file missing.md",
		"gh pr create --body-file -",
		"git commit -F notes.txt",
		"git commit -m 'test: a ghp_FIXTUREfixtureFIXTUREfixture value is let through'",
		"git commit -m 'test: ghp_aaaaBQFgtZmKcrudPwvbxNh is synthetic'",
		"git commit -m 'test: ghp_abcdefBQFgtZmKcrudPwvbxNh is synthetic'",
		"gh api repos/o/r/issues -F title=fix -F body=@notes.txt",
		"gh api repos/o/r/issues -F body=@missing.md",
		"gh api repos/o/r/issues -f body=@body.md",
		"gh release create v1 --notes-file notes.txt",
		"gh gist create -d body.md notes.txt",
		"gh gist create --filename body.md - <<< hello",
		"echo $GH_TOKEN | gh auth login --with-token",
		"git log --grep 'token=' --oneline",
		"git push origin feat/creds",
		// not gh or git: what they do with a value is not this rule's
		"printf '%s' " + githubToken + " > ~/.config/token",
		"grep -rn " + anthropicKey + " .",
	} {
		if reason := Check(bashIn(dir, command)); reason != "" {
			t.Errorf("refused %q: %s", command, reason)
		}
	}
}

// TestSyntheticMatchesTheSecretRules: the allowance, value by value, as
// secret-rules.awk judges it.
func TestSyntheticMatchesTheSecretRules(t *testing.T) {
	for value, want := range map[string]bool{
		invented:                   false,
		"ghp_" + invented:          false,
		"ghp_FAKEBQFgtZmKcrudPwvb": true,
		"BQFgtExampleKcrudPwvbxNh": true,
		"BQFgtZZZZmKcrudPwvbxNhel": true,
		"BQFgtZZZmKcrudPwvbxNhelo": false,
		"BQFgtZmKcrud456789Pwvbxh": true,
		"BQFgtZmKcrud45678Pwvbxhe": false,
		"BQFgtZmKcrudponmlkPwvbxh": true,
		"BQFgtZmKcrudxyzABCPwvbxh": true,
		"BQFgtZmKcrud9abcdePwvbxh": true,
	} {
		if got := synthetic(value); got != want {
			t.Errorf("synthetic(%q) = %v, want %v", value, got, want)
		}
	}
}

// TestCredentialShapesMatchTheSecretRules: the hook's shapes are the
// credential shapes of scripts/secret-rules.awk, pattern for pattern, so
// a shape added there is refused here the same day.
func TestCredentialShapesMatchTheSecretRules(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "scripts", "secret-rules.awk"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assignment := regexp.MustCompile(`(name|regex|only)\[(\d+)\] = "((?:[^"\\]|\\.)*)"`)
	rules := map[string]map[string]string{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		for _, found := range assignment.FindAllStringSubmatch(lines.Text(), -1) {
			if rules[found[2]] == nil {
				rules[found[2]] = map[string]string{}
			}
			rules[found[2]][found[1]] = strings.ReplaceAll(found[3], `\"`, `"`)
		}
	}
	want := map[string]string{}
	for _, rule := range rules {
		if rule["only"] == "" {
			want[rule["name"]] = rule["regex"]
		}
	}
	got := map[string]string{}
	for _, shape := range credentialShapes {
		got[shape.name] = shape.pattern.String()
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if len(want) == 0 || string(gotJSON) != string(wantJSON) {
		t.Fatalf("the hook's shapes:\n%s\nsecret-rules.awk's credential shapes:\n%s", gotJSON, wantJSON)
	}
}
