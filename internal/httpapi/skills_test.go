package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/promptschat"
	"github.com/enes-alatas/spool/internal/store"
)

var oneSkill = []promptschat.Skill{{Dir: "headers", Files: []promptschat.SkillFile{
	{Path: "SKILL.md", Content: "---\nname: headers\n---\nRead them."},
	{Path: "references/rules.md", Content: "rules"},
}}}

func refusalCode(refusal *requestError) string {
	if refusal == nil {
		return ""
	}
	return refusal.code
}

// A create's skills are held to the draft's rules again, and refused for
// a loop that can't take them: a cloud loop, a bare loop in a directory
// of the operator's, and a bare loop whose workdir has the folder.
func TestCheckSkillsRefusesWhatALoopCantTake(t *testing.T) {
	home := t.TempDir()
	taken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taken, ".claude", "skills", "headers"), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []promptschat.Skill{{Dir: "headers", Files: []promptschat.SkillFile{{Path: "SKILL.md", Content: "x"}, {Path: "run.sh", Content: "y"}}}}
	for name, test := range map[string]struct {
		loop   store.Loop
		skills []promptschat.Skill
		want   string
	}{
		"bare, its own home":     {store.Loop{Runtime: store.RuntimeBare, WorkspaceMode: store.WorkspaceNone, WorkspacePath: home}, oneSkill, ""},
		"bare, its own worktree": {store.Loop{Runtime: store.RuntimeBare, WorkspaceMode: store.WorkspaceWorktree, WorkspacePath: home}, oneSkill, ""},
		"docker":                 {store.Loop{Runtime: store.RuntimeDocker, WorkspacePath: "/home/loop"}, oneSkill, ""},
		"no skills, cloud":       {store.Loop{Runtime: store.RuntimeCloud}, nil, ""},
		"a script":               {store.Loop{Runtime: store.RuntimeDocker}, bad, codeSkillsInvalid},
		"cloud":                  {store.Loop{Runtime: store.RuntimeCloud}, oneSkill, codeSkillsUnsupported},
		"bare, a dir of yours":   {store.Loop{Runtime: store.RuntimeBare, WorkspaceMode: store.WorkspaceDir, WorkspacePath: home}, oneSkill, codeSkillsUnsupported},
		"bare, folder there":     {store.Loop{Runtime: store.RuntimeBare, WorkspaceMode: store.WorkspaceNone, WorkspacePath: taken}, oneSkill, codeSkillsExist},
	} {
		got := refusalCode(checkSkillRules(&test.loop, test.skills))
		if got == "" {
			got = refusalCode(checkSkillPlace(&test.loop, test.skills))
		}
		if got != test.want {
			t.Errorf("%s: refusal %q; want %q", name, got, test.want)
		}
	}
}

// Staging writes each file under the loop's stage, byte for byte, and
// unstaging drops it.
func TestStageSkillsWritesTheDraftsBytes(t *testing.T) {
	server := &Server{DataDir: t.TempDir()}
	loopRecord := &store.Loop{ID: "l1"}
	if err := server.stageSkills(loopRecord, oneSkill); err != nil {
		t.Fatal(err)
	}
	stage := loop.StagedSkillsDir(server.DataDir, "l1")
	for _, file := range oneSkill[0].Files {
		got, err := os.ReadFile(filepath.Join(stage, "headers", filepath.FromSlash(file.Path)))
		if err != nil || string(got) != file.Content {
			t.Errorf("%s = %q, %v; want %q", file.Path, got, err, file.Content)
		}
	}
	if got, err := os.ReadFile(filepath.Join(stage, "headers", ".gitignore")); err != nil || !strings.HasSuffix(string(got), "\n*\n") {
		t.Errorf("the skill's .gitignore = %q, %v; want one that ignores everything", got, err)
	}
	server.unstageSkills(loopRecord)
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("the stage is still there after unstaging: %v", err)
	}
	if err := server.stageSkills(&store.Loop{ID: "l2"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(loop.StagedSkillsDir(server.DataDir, "l2")); !os.IsNotExist(err) {
		t.Error("no skills staged a folder")
	}
}

// Only the two creates take the larger body.
func TestCreateRoutesTakeTheLargerBody(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/loops", true},
		{"POST", "/api/import/prompts-chat/team", true},
		{"PUT", "/api/loops", false},
		{"POST", "/api/loops/x/messages", false},
		{"POST", "/api/session", false},
		{"GET", "/api/import/prompts-chat/team", false},
	} {
		if got := createRoute(test.method, test.path); got != test.want {
			t.Errorf("createRoute(%s %s) = %v; want %v", test.method, test.path, got, test.want)
		}
	}
}
