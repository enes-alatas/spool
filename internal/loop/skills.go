package loop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/enes-alatas/spool/internal/runtime"
)

// SkillsDir is where a loop's skills go, under its workdir: the project's
// skills, never the user's ~/.claude/skills, which a bare loop shares with
// every other bare loop and the operator's own Claude Code (#1364).
const SkillsDir = ".claude/skills"

// StagedSkillsDir is where the skills a loop was created with wait under
// the hub's data dir for its first wake to install (#1364).
func StagedSkillsDir(dataDir, loopID string) string {
	return filepath.Join(dataDir, "skills", loopID)
}

// carrySkills installs the skills staged for the loop in its workdir,
// before its session starts, and drops the stage once all of it is there.
// A workstation gets each file through the runtime; on the host each is
// written inside the workdir only, never over a file already there and
// never through a link. A copy that fails keeps the stage for the next
// wake to try again.
func (actor *Actor) carrySkills(ctx context.Context, loopRuntime runtime.Runtime, workDir string) {
	if actor.deps.DataDir == "" || workDir == "" {
		return
	}
	staged := StagedSkillsDir(actor.deps.DataDir, actor.loop.ID)
	if _, err := os.Stat(staged); err != nil {
		return
	}
	var err error
	if loopRuntime.HasWorkstation() {
		// PutFile also sweeps files over 30 days old from the target's
		// folder, a cleanup built for the attachment inbox. A first install
		// writes only new files, so it removes nothing; an install into a
		// skill folder that already exists must not come through here.
		err = walkStaged(staged, func(file, rel string) error {
			return loopRuntime.PutFile(ctx, actor.loop.ID, file, path.Join(workDir, SkillsDir, rel))
		})
	} else {
		var kept []string
		kept, err = installOnHost(staged, workDir)
		if len(kept) > 0 {
			// Create refuses a skill whose folder is already there, so a
			// file found here was written by an earlier wake whose install
			// stopped partway, or by the loop itself since. Neither is
			// written over; the stage is dropped all the same.
			actor.log().Info("skill files a previous wake installed were kept", "files", kept)
		}
	}
	if err != nil {
		actor.log().Warn("skills not installed in the loop's workdir; the next wake retries", "err", err)
		return
	}
	actor.storeSpoolEvent("skills_installed", "{}")
	_ = os.RemoveAll(staged)
}

// walkStaged calls put for each regular file under staged, with its path
// relative to staged, in /.
func walkStaged(staged string, put func(file, rel string) error) error {
	return filepath.WalkDir(staged, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(staged, file)
		if err != nil {
			return err
		}
		return put(file, filepath.ToSlash(rel))
	})
}

// installOnHost copies the staged skills into workDir's SkillsDir. The
// writes go through an os.Root on workDir, so no link leads out of it,
// and each file is created new: one already there is left as it is, and
// named in kept.
func installOnHost(staged, workDir string) (kept []string, err error) {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	err = walkStaged(staged, func(file, rel string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		name := path.Join(SkillsDir, rel)
		if err := root.MkdirAll(path.Dir(name), 0o700); err != nil {
			return err
		}
		target, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			kept = append(kept, name)
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := target.Write(data); err != nil {
			_ = target.Close()
			return err
		}
		return target.Close()
	})
	if err != nil {
		return kept, fmt.Errorf("install skills: %w", err)
	}
	return kept, nil
}
