package httpapi

import (
	"errors"
	"io/fs"
	"os"
	"path"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/promptschat"
	"github.com/enes-alatas/spool/internal/store"
)

// The refusals of a create's skills (#1364).
const (
	// codeSkillsInvalid is a skill that breaks a rule a draft's skills are
	// held to: create installs what it is sent, so it checks it all again.
	codeSkillsInvalid = "skills_invalid"
	// codeSkillsUnsupported is skills sent for a loop that can't take
	// them: a cloud loop, or a bare loop in the operator's own directory,
	// which other loops may share.
	codeSkillsUnsupported = "skills_unsupported"
	// codeSkillsExist is a skill whose folder the loop's workdir already
	// has: an install never writes over what is there.
	codeSkillsExist = "skills_exist"
)

// maxCreateBody bounds a create's body, which carries the skills the
// operator ticked as well as the missions: a team's missions alone reach
// 768 KiB, and each loop's skills up to 1 MiB (#1364).
const maxCreateBody = 4 << 20

// createRoute reports whether method and urlPath are a create that takes
// skills, whose body gets maxCreateBody rather than maxAPIBody.
func createRoute(method, urlPath string) bool {
	return method == "POST" && (urlPath == "/api/loops" || urlPath == "/api/import/prompts-chat/team")
}

// checkSkillRules refuses skills that break a draft's rules, and any for
// a cloud loop, before the loop's workspace is made.
func checkSkillRules(loopRecord *store.Loop, skills []promptschat.Skill) *requestError {
	if len(skills) == 0 {
		return nil
	}
	if err := promptschat.CheckSkills(skills); err != nil {
		return refusalf(400, codeSkillsInvalid, "%v", err)
	}
	if loopRecord.Runtime == store.RuntimeCloud {
		return refusalf(400, codeSkillsUnsupported, "a cloud loop takes no skills from an import")
	}
	return nil
}

// checkSkillPlace refuses skills loopRecord's workspace, made but not
// stored, can't take: any for a bare loop in a directory of the
// operator's, and one whose folder the bare loop's workdir already holds.
// A docker loop's workstation is new.
func checkSkillPlace(loopRecord *store.Loop, skills []promptschat.Skill) *requestError {
	if len(skills) == 0 {
		return nil
	}
	switch {
	case loopRecord.Runtime == store.RuntimeDocker:
		return nil
	case loopRecord.WorkspaceMode == store.WorkspaceDir:
		return refusalf(400, codeSkillsUnsupported, "a loop working in a directory of yours takes no skills from an import, since other loops may share it; give it no workspace or a git repo")
	}
	// looked up inside the workdir only: a link on the way that leads out
	// of it refuses the skill as if its folder were there
	root, err := os.OpenRoot(loopRecord.WorkspacePath)
	if err != nil {
		return refusalf(500, codeSkillsUnsupported, "the loop's workdir can't be read")
	}
	defer func() { _ = root.Close() }()
	for _, item := range skills {
		if _, err := root.Lstat(path.Join(loop.SkillsDir, item.Dir)); !errors.Is(err, fs.ErrNotExist) {
			return refusalf(409, codeSkillsExist, "the loop's workdir already has %s/%s; an import never writes over it", loop.SkillsDir, item.Dir)
		}
	}
	return nil
}

// stageSkills writes skills under the data dir for loopRecord's first
// wake to install in its workdir, before its session starts. The paths
// are the ones checkSkills passed, written inside the stage alone.
func (server *Server) stageSkills(loopRecord *store.Loop, skills []promptschat.Skill) error {
	if len(skills) == 0 {
		return nil
	}
	stage := loop.StagedSkillsDir(server.DataDir, loopRecord.ID)
	err := writeSkills(stage, skills)
	if err != nil {
		_ = os.RemoveAll(stage)
	}
	return err
}

// unstageSkills drops what stageSkills wrote, for a create that stored
// nothing.
func (server *Server) unstageSkills(loopRecord *store.Loop) {
	_ = os.RemoveAll(loop.StagedSkillsDir(server.DataDir, loopRecord.ID))
}

func writeSkills(stage string, skills []promptschat.Skill) error {
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, item := range skills {
		for _, file := range item.Files {
			name := path.Join(item.Dir, file.Path)
			if err := root.MkdirAll(path.Dir(name), 0o700); err != nil {
				return err
			}
			if err := root.WriteFile(name, []byte(file.Content), 0o600); err != nil {
				return err
			}
		}
		// A bare loop's worktree is the operator's repo: the skill would be
		// untracked files there, which a loop that commits everything
		// commits and pushes. A .gitignore of * in the skill's own folder
		// keeps all of it, itself included, out of git, and touches no
		// file of the operator's. A skill brings only .md and .txt, so the
		// name is the hub's alone.
		if err := root.WriteFile(path.Join(item.Dir, skillIgnoreFile), []byte(skillIgnore), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// skillIgnoreFile and skillIgnore are written in each installed skill's
// folder, to keep the skill out of the git repo a loop works in (#1364).
const (
	skillIgnoreFile = ".gitignore"
	skillIgnore     = "# installed by Spool from a prompts.chat import; kept out of git\n*\n"
)
