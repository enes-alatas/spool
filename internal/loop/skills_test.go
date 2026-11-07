package loop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func stageFile(t *testing.T, stage, rel, content string) {
	t.Helper()
	file := filepath.Join(stage, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A staged skill lands under the workdir's .claude/skills, byte for byte.
func TestInstallOnHostWritesTheStagedSkills(t *testing.T) {
	stage, workDir := t.TempDir(), t.TempDir()
	stageFile(t, stage, "headers/SKILL.md", "---\nname: headers\n---\nRead them.")
	stageFile(t, stage, "headers/references/rules.md", "rules")
	kept, err := installOnHost(stage, workDir)
	if err != nil || len(kept) != 0 {
		t.Fatalf("installOnHost = %v, %v", kept, err)
	}
	for rel, want := range map[string]string{
		".claude/skills/headers/SKILL.md":            "---\nname: headers\n---\nRead them.",
		".claude/skills/headers/references/rules.md": "rules",
	} {
		got, err := os.ReadFile(filepath.Join(workDir, rel))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
}

// A file already in the workdir is left as it is, and named.
func TestInstallOnHostNeverWritesOver(t *testing.T) {
	stage, workDir := t.TempDir(), t.TempDir()
	stageFile(t, stage, "headers/SKILL.md", "new")
	stageFile(t, workDir, ".claude/skills/headers/SKILL.md", "mine")
	kept, err := installOnHost(stage, workDir)
	if err != nil || !reflect.DeepEqual(kept, []string{".claude/skills/headers/SKILL.md"}) {
		t.Fatalf("installOnHost = %v, %v; want the file kept", kept, err)
	}
	if got, _ := os.ReadFile(filepath.Join(workDir, ".claude/skills/headers/SKILL.md")); string(got) != "mine" {
		t.Errorf("the file there = %q; want it untouched", got)
	}
}

// A link in the workdir that leads out of it is never written through.
func TestInstallOnHostWritesNothingThroughALinkOut(t *testing.T) {
	stage, workDir, outside := t.TempDir(), t.TempDir(), t.TempDir()
	stageFile(t, stage, "headers/SKILL.md", "x")
	if err := os.Symlink(outside, filepath.Join(workDir, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := installOnHost(stage, workDir); err == nil {
		t.Error("installOnHost wrote through a link out of the workdir")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the link's target holds %d entries; want none", len(entries))
	}
}
