//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// skillSite stands in for prompts.chat with one bot linking skills, whose
// first skill's text can change after the draft is read. It counts the
// fetches of each path.
type skillSite struct {
	mu      sync.Mutex
	headers string
	asked   map[string]*atomic.Int32
}

func (site *skillSite) setHeaders(content string) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.headers = content
}

func (site *skillSite) fetches(path string) int32 {
	site.mu.Lock()
	defer site.mu.Unlock()
	if count, ok := site.asked[path]; ok {
		return count.Load()
	}
	return 0
}

func startSkillSite(t *testing.T) (*skillSite, string) {
	t.Helper()
	site := &skillSite{
		headers: "---\nname: http-headers\n---\nCheck the headers.\x1fFILE:references/rules.md\x1e\nHSTS first.",
		asked:   map[string]*atomic.Int32{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		site.mu.Lock()
		if site.asked[request.URL.Path] == nil {
			site.asked[request.URL.Path] = &atomic.Int32{}
		}
		site.asked[request.URL.Path].Add(1)
		headers := site.headers
		site.mu.Unlock()
		var body []byte
		switch request.URL.Path {
		case "/bots/cskilled_header-checker.json":
			read, err := os.ReadFile(filepath.Join("..", "internal", "promptschat", "testdata", "bot-skilled.json"))
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
			body = read
		case "/api/prompts/cskill0001":
			body, _ = json.Marshal(map[string]string{"type": "SKILL", "slug": "http-headers", "content": headers})
		case "/api/prompts/cskill0002":
			body, _ = json.Marshal(map[string]string{"type": "SKILL", "slug": "kind-tone", "content": "---\nname: kind-tone\n---\nBe kind."})
		case "/api/prompts/cskill0003":
			body, _ = json.Marshal(map[string]string{"type": "SKILL", "slug": "scanner", "content": "Scan.\x1fFILE:scripts/scan.py\x1e\nprint(1)"})
		default:
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)
	return site, server.URL
}

type draftedSkill struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
	Dir       string `json:"dir"`
	Files     []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

// A bot whose skills link prompts.chat skills is drafted with their files
// (#1364). Create installs the skills the operator kept, from the bytes
// the draft showed, in the loop's workdir .claude/skills/ before its first
// session: a skill that changed on prompts.chat after the draft is
// installed as it was read, with no second fetch, and a skill left out of
// the create puts no file on disk. A script-carrying skill and a
// look-alike host are listed as not installed, and the look-alike is never
// fetched.
func TestAnImportedBotsLinkedSkillsAreInstalled(t *testing.T) {
	t.Parallel()
	site, base := startSkillSite(t)
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare", "--prompts-chat-base", base)

	var draft struct {
		Name    string         `json:"name"`
		Mission string         `json:"mission"`
		Skills  []draftedSkill `json:"skills"`
	}
	hub.mustJSON("GET", "/api/import/prompts-chat?url="+url.QueryEscape("https://prompts.chat/bots/cskilled_header-checker"), nil, &draft)
	if len(draft.Skills) != 4 {
		t.Fatalf("the draft's skills = %+v; want 4", draft.Skills)
	}
	headers, tone, scanner, lookalike := draft.Skills[0], draft.Skills[1], draft.Skills[2], draft.Skills[3]
	if !headers.Installed || headers.Dir != "http-headers" || len(headers.Files) != 2 ||
		headers.Files[0].Path != "SKILL.md" || headers.Files[1].Path != "references/rules.md" || headers.Files[1].Content != "HSTS first." ||
		headers.Source != "https://prompts.chat/prompts/cskill0001_http-headers" {
		t.Errorf("the linked skill's draft = %+v", headers)
	}
	if !tone.Installed || tone.Dir != "kind-tone" {
		t.Errorf("the second linked skill's draft = %+v", tone)
	}
	if scanner.Installed || scanner.Reason != "it carries files that aren't .md or .txt" || len(scanner.Files) != 0 {
		t.Errorf("the script-carrying skill = %+v; want not installed", scanner)
	}
	if lookalike.Installed || lookalike.Reason != "prompts.chat publishes no files for it" || lookalike.Source != "" {
		t.Errorf("the look-alike host's skill = %+v; want not installed, no source", lookalike)
	}
	if !strings.Contains(draft.Mission, "- Scanner: Runs a scan.") || strings.Contains(draft.Mission, "- Headers:") {
		t.Errorf("the mission names an installed skill, or not a missing one:\n%s", draft.Mission)
	}

	// prompts.chat changes the skill after the operator read it
	site.setHeaders("---\nname: http-headers\n---\nSomething else entirely.")
	read := headers.Files[0].Content

	// the operator keeps Headers and leaves Tone out
	hub.createLoop(draft.Name, map[string]any{
		"mission": draft.Mission, "runtime": "bare",
		"skills": []map[string]any{{"dir": headers.Dir, "files": headers.Files}},
	})
	hub.waitTurn(draft.Name, firstTurnWait, func(turn) bool { return true })

	skills := filepath.Join(dataDir, "homes", draft.Name, ".claude", "skills")
	got, err := os.ReadFile(filepath.Join(skills, "http-headers", "SKILL.md"))
	if err != nil || string(got) != read {
		t.Errorf("the installed SKILL.md = %q, %v; want the bytes the draft showed, %q", got, err, read)
	}
	if got, err := os.ReadFile(filepath.Join(skills, "http-headers", "references", "rules.md")); err != nil || string(got) != "HSTS first." {
		t.Errorf("the installed reference = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(skills, "kind-tone")); !os.IsNotExist(err) {
		t.Errorf("a skill left out of the create is on disk: %v", err)
	}
	if fetched := site.fetches("/api/prompts/cskill0001"); fetched != 1 {
		t.Errorf("the kept skill was fetched %d times; want once, for the draft", fetched)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "skills")); err == nil {
		if entries, _ := os.ReadDir(filepath.Join(dataDir, "skills")); len(entries) != 0 {
			t.Errorf("the stage holds %d entries after the install; want none", len(entries))
		}
	}

	// a create whose skill breaks a rule makes nothing
	resp, body := hub.do("POST", "/api/loops", map[string]any{
		"name": "bad-skills", "mission": "m", "runtime": "bare",
		"skills": []map[string]any{{"dir": "x1", "files": []map[string]string{{"path": "SKILL.md", "content": "x"}, {"path": "../run.md", "content": "y"}}}},
	})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "skills_invalid") {
		t.Errorf("a create with a .. path = %d %s; want 400 skills_invalid", resp.StatusCode, body)
	}
	if resp, _ := hub.do("GET", "/api/loops/bad-skills", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the refused create made a loop: %d", resp.StatusCode)
	}
}

// dockerLoopGetsItsImportedSkills: a docker loop created with skills has
// them in its workstation's .claude/skills/ by its first turn, carried in
// from the stage, which is then dropped (#1364).
func dockerLoopGetsItsImportedSkills(t *testing.T) {
	dataDir := t.TempDir()
	hub := startDockerServer(t, dataDir)
	files := []map[string]string{{"path": "SKILL.md", "content": "---\nname: kind-tone\n---\nBe kind."}, {"path": "references/a.md", "content": "Say please."}}
	hub.createLoop("skilled", map[string]any{"skills": []map[string]any{{"dir": "kind-tone", "files": files}}})
	hub.waitTurn("skilled", firstTurnWait, func(turn) bool { return true })
	loopID := hub.loop("skilled").ID
	for _, file := range files {
		out, err := exec.Command("docker", "exec", "spool-ws-"+loopID, "cat", "/home/loop/.claude/skills/kind-tone/"+file["path"]).CombinedOutput()
		if err != nil || string(out) != file["content"] {
			t.Errorf("%s in the workstation = %q (%v); want %q", file["path"], out, err, file["content"])
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "skills", loopID)); !os.IsNotExist(err) {
		t.Errorf("the staged skills outlived their carry: %v", err)
	}
}

// A bare loop in a git worktree, the operator's repo, gets its imported
// skill kept out of git: after the install the worktree's status is
// clean, so a loop that commits everything commits none of it (#1364).
func TestAnImportedSkillStaysOutOfALoopsRepo(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare")
	files := []map[string]string{{"path": "SKILL.md", "content": "---\nname: kind-tone\n---\nBe kind."}, {"path": "references/a.md", "content": "Say please."}}
	hub.createLoop("repo-skilled", map[string]any{
		"runtime": "bare", "workspace_path": repo,
		"skills": []map[string]any{{"dir": "kind-tone", "files": files}},
	})
	hub.waitTurn("repo-skilled", firstTurnWait, func(turn) bool { return true })
	worktree := hub.loop("repo-skilled").WorkspacePath
	for _, file := range files {
		got, err := os.ReadFile(filepath.Join(worktree, ".claude", "skills", "kind-tone", filepath.FromSlash(file["path"])))
		if err != nil || string(got) != file["content"] {
			t.Errorf("%s in the worktree = %q, %v; want %q", file["path"], got, err, file["content"])
		}
	}
	if status := gitOut(t, worktree, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Errorf("the worktree's status after the install = %q; want clean", status)
	}
}
