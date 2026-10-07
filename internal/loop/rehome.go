package loop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// Rehoming moves a bare loop into a docker workstation (#624). The move is a
// context rotation with its own cause (ADR-0022): the loop's last turn on the
// host writes a handoff note, the row turns into a docker loop's, and the
// fresh session starts in the workstation. The loop keeps its name, bots,
// channels, history and auto-memory; nothing else of the host's goes with it.

// ErrNotBare is a rehome asked of a loop that is not on the bare runtime.
// Rehoming goes one way only: a docker loop never becomes bare (ADR-0018).
var ErrNotBare = errors.New("only a bare loop can be rehomed")

// The size a rehomed loop's workstation gets, the same a docker loop is
// created with when the operator names none.
const (
	rehomeMemMB = 4096
	rehomeCPUs  = 2
)

// workstationMemoryDir is where claude keeps its auto-memory inside a
// workstation: its config dir is the home's .claude, and its project is the
// home itself, the cwd every wake runs in.
var workstationMemoryDir = path.Join(runtime.WorkstationHome, ".claude", "projects",
	projectSlug(runtime.WorkstationHome), "memory")

// rehome turns loopRecord, a bare loop whose session has ended, into a docker
// loop. Its auto-memory is staged under the hub's data dir first, for the
// next wake to carry into the workstation; a memory that cannot be staged is
// logged and left behind rather than holding the move up. It returns the
// host workspace the loop no longer uses, "" for none.
func rehome(ctx context.Context, deps *Deps, loopRecord *store.Loop) (string, error) {
	if loopRecord.Runtime != store.RuntimeBare && loopRecord.Runtime != "" {
		return "", ErrNotBare
	}
	if source := claudeMemoryDir(loopRecord.WorkspacePath); source != "" && deps.DataDir != "" {
		if err := stageMemory(source, stagedMemoryDir(deps.DataDir, loopRecord.ID)); err != nil {
			deps.log().Warn("auto-memory not staged for the workstation", "loop", loopRecord.Name, "err", err)
		}
	}
	memMB, cpus := loopRecord.MemMB, loopRecord.CPUs
	if memMB == 0 {
		memMB = rehomeMemMB
	}
	if cpus == 0 {
		cpus = rehomeCPUs
	}
	if err := deps.Store.Loops().Rehome(ctx, loopRecord.ID, runtime.WorkstationHome, memMB, cpus, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	left := loopRecord.WorkspacePath
	loopRecord.Runtime = store.RuntimeDocker
	loopRecord.WorkspaceMode = store.WorkspaceNone
	loopRecord.WorkspacePath = runtime.WorkstationHome
	loopRecord.RepoPath, loopRecord.WorktreePath, loopRecord.Branch = "", "", ""
	loopRecord.MemMB, loopRecord.CPUs = memMB, cpus
	return left, nil
}

// rehomeDue reports whether loopRecord was asked to rehome and has not moved
// yet: the rotation that moves it is recorded with its reason, and a loop
// still on the host has not taken it.
func rehomeDue(loopRecord *store.Loop) bool {
	return loopRecord.RotateReason == store.RotationReasonRehome &&
		(loopRecord.Runtime == store.RuntimeBare || loopRecord.Runtime == "")
}

// claudeMemoryDir is where claude keeps the auto-memory of a session run in
// workDir on this host, "" when there is no workspace to key one by. Claude
// keys it by the project: the root of the git repo the workspace is in,
// shared by every worktree of it, or else the workspace itself.
func claudeMemoryDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(configDir, "projects", projectSlug(projectRoot(workDir)), "memory")
}

// projectRoot is the directory claude names a session's project after: the
// main checkout of the repo workDir is in, or workDir outside one.
func projectRoot(workDir string) string {
	out, err := exec.Command("git", "-C", workDir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return workDir
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) != ".git" {
		// a bare repository has no checkout to name it after
		return workDir
	}
	return filepath.Dir(common)
}

// projectSlug is the name claude gives a project's directory: its path with
// every character that is not a letter or digit made a hyphen.
func projectSlug(dir string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, dir)
}

// stagedMemoryDir is where a rehomed loop's auto-memory waits under the
// hub's data dir for its workstation.
func stagedMemoryDir(dataDir, loopID string) string {
	return filepath.Join(dataDir, "rehome", loopID, "memory")
}

// maxStagedMemory bounds what a rehome copies: an auto-memory is a few dozen
// small notes, and a directory past this is not one.
const maxStagedMemory = 16 << 20

// stageMemory copies the regular files under source into target, keeping
// their relative paths. A missing source is no memory to stage.
func stageMemory(source, target string) error {
	var total int64
	err := filepath.WalkDir(source, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && file == source {
				return fs.SkipAll
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > maxStagedMemory {
			return fmt.Errorf("auto-memory is over %d bytes", maxStagedMemory)
		}
		rel, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o600)
	})
	if err != nil {
		_ = os.RemoveAll(target)
	}
	return err
}

// carryMemory puts a staged auto-memory into the loop's workstation, and
// drops the staged copy once all of it is there. A copy that fails keeps it
// for the next wake to try again.
func (actor *Actor) carryMemory(ctx context.Context, loopRuntime runtime.Runtime) {
	if actor.deps.DataDir == "" || !loopRuntime.HasWorkstation() {
		return
	}
	staged := stagedMemoryDir(actor.deps.DataDir, actor.loop.ID)
	err := filepath.WalkDir(staged, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && file == staged {
				return fs.SkipAll
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(staged, file)
		if err != nil {
			return err
		}
		return loopRuntime.PutFile(ctx, actor.loop.ID, file, path.Join(workstationMemoryDir, filepath.ToSlash(rel)))
	})
	if err != nil {
		actor.log().Warn("auto-memory not carried into the workstation; the next wake retries", "err", err)
		return
	}
	if _, statErr := os.Stat(staged); statErr == nil {
		actor.storeSpoolEvent("memory_carried", "{}")
	}
	_ = os.RemoveAll(filepath.Dir(staged))
}
