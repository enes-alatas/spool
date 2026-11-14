package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
)

// scriptPATH is the PATH the scripts get when they run as the hub's
// user, from runuser's empty environment, with their HOME (the scripts
// read it under set -u).
const scriptPATH = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// scriptUser is who runs the backup and restore scripts (ADR-0057 item 8,
// #1583): the unit's User= when the upgrade runs under a unit, so
// no script, the target's or the installed one, runs as root; nil, the
// upgrade's own user, for a unit with no User= or a run with --no-unit.
func (up *upgrader) scriptUser(u hubUnit) (*user.User, error) {
	if u.Manual || u.User == "" || u.User == "root" {
		return nil, nil
	}
	who, err := up.lookupUser(u.User)
	if err != nil {
		return nil, fmt.Errorf("the unit's User=%s: %w", u.User, err)
	}
	if who.Uid == "0" {
		return nil, nil
	}
	return who, nil
}

// restoreUser is who runs the restore: the script user when the data
// directory's parent is theirs, as the restore makes the directory and
// its staging there; root, as before #1583, in a layout where it isn't.
func (up *upgrader) restoreUser(u hubUnit) (*user.User, error) {
	who, err := up.scriptUser(u)
	if who == nil || err != nil {
		return who, err
	}
	owner, err := up.ownerOf(filepath.Dir(u.DataDir))
	if err != nil || owner.Uid != who.Uid {
		return nil, nil
	}
	return who, nil
}

// scriptCmd is a script's command line, as who when who is set.
func scriptCmd(who *user.User, name string, args []string) hostCmd {
	if who == nil {
		return hostCmd{Name: name, Args: args}
	}
	return hostCmd{User: who.Username, Env: []string{scriptPATH, "HOME=" + who.HomeDir}, Name: name, Args: args}
}

// handOff makes a fresh directory beside dir, mode 0700, that only who
// can enter.
func handOff(dir string, who *user.User) (string, error) {
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".spool-upgrade-*")
	if err != nil {
		return "", err
	}
	uid, gid, err := ids(who)
	if err == nil {
		err = os.Lchown(stage, uid, gid)
	}
	if err != nil {
		_ = os.RemoveAll(stage)
		return "", err
	}
	return stage, nil
}

func ids(who *user.User) (int, int, error) {
	uid, err := strconv.Atoi(who.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("uid %q of %s: %w", who.Uid, who.Username, err)
	}
	gid, err := strconv.Atoi(who.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("gid %q of %s: %w", who.Gid, who.Username, err)
	}
	return uid, gid, nil
}

// takeIn copies a file the script wrote in stage into dir, mode 0600,
// as the upgrade's own user. It reads the file through a descriptor
// that refuses a symlink and takes a regular file only, so the hub's
// user can't point root at anything else.
func takeIn(stage, path, dir string) (string, error) {
	if filepath.Dir(filepath.Clean(path)) != stage {
		return "", fmt.Errorf("the backup named %s, which is not in %s", path, stage)
	}
	in, err := openNoFollow(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	if info, err := in.Stat(); err != nil {
		return "", err
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the backup named %s, which is not a regular file", path)
	}
	dst := filepath.Join(dir, filepath.Base(path))
	return dst, writeAtomic(in, dst, 0o600, -1, -1)
}

// handOver copies src into stage as who's, mode 0600, for a script
// running as who to read.
func handOver(src, stage string, who *user.User) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	uid, gid, err := ids(who)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(stage, filepath.Base(src))
	return dst, writeAtomic(in, dst, 0o600, uid, gid)
}

// writeAtomic writes in to dst through a scratch file beside it, synced
// before the rename, and given to uid:gid first unless uid is -1 (through
// the descriptor, so no path the hub's user can swap is followed).
func writeAtomic(in io.Reader, dst string, mode os.FileMode, uid, gid int) error {
	scratch, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(scratch.Name()) }()
	_, err = io.Copy(scratch, in)
	if err == nil {
		err = scratch.Chmod(mode)
	}
	if err == nil && uid != -1 {
		err = scratch.Chown(uid, gid)
	}
	if err == nil {
		err = scratch.Sync()
	}
	if closeErr := scratch.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(scratch.Name(), dst)
}

// backUp runs the backup into up.backupDir, as scriptUser when there is
// one: it writes into a directory of theirs beside up.backupDir, and the
// upgrade takes in each file it names as its own, then removes that
// directory (#1583).
func (up *upgrader) backUp(ctx context.Context, u hubUnit) (dataFile, keysFile string, err error) {
	who, err := up.scriptUser(u)
	if err != nil {
		return "", "", err
	}
	out := up.backupDir
	if who != nil {
		if out, err = handOff(up.backupDir, who); err != nil {
			return "", "", fmt.Errorf("making a directory for %s to back up into: %w", who.Username, err)
		}
		defer func() { _ = os.RemoveAll(out) }()
	}
	said, err := up.host.Run(ctx, scriptCmd(who, up.backupPath, []string{"--data-dir", u.DataDir, "--out", out, "--no-volumes"}))
	if err != nil {
		return "", "", err
	}
	dataFile, keysFile = backupFiles(said)
	if dataFile == "" {
		return "", "", fmt.Errorf("%s named no data file:\n%s", up.backupPath, said)
	}
	if who == nil {
		return dataFile, keysFile, nil
	}
	if dataFile, err = takeIn(out, dataFile, up.backupDir); err != nil {
		return "", "", err
	}
	if keysFile != "" {
		if keysFile, err = takeIn(out, keysFile, up.backupDir); err != nil {
			return "", "", err
		}
	}
	return dataFile, keysFile, nil
}

// restore runs the restore of a backup in up.backupDir, as restoreUser
// when there is one, from copies handed over to them.
func (up *upgrader) restore(ctx context.Context, u hubUnit, args []string, files ...string) error {
	who, err := up.restoreUser(u)
	if err != nil {
		return err
	}
	if who != nil {
		stage, err := handOff(up.backupDir, who)
		if err != nil {
			return fmt.Errorf("making a directory for %s to restore from: %w", who.Username, err)
		}
		defer func() { _ = os.RemoveAll(stage) }()
		args = slices.Clone(args)
		for _, file := range files {
			copied, err := handOver(file, stage, who)
			if err != nil {
				return err
			}
			for i := range args {
				if args[i] == file {
					args[i] = copied
				}
			}
		}
	}
	_, err = up.host.Run(ctx, scriptCmd(who, up.restorePath, args))
	return err
}

// scriptsAs is the plan's word on who runs the scripts.
func (up *upgrader) scriptsAs(u hubUnit) string {
	who, err := up.scriptUser(u)
	if err != nil || who == nil {
		return ""
	}
	how := ", as " + who.Username + " into a directory of theirs beside it, each file then taken in as root's, mode 0600; a rollback's restore runs as " + who.Username + " too"
	if rw, _ := up.restoreUser(u); rw == nil {
		how += ", but as root here, as " + filepath.Dir(u.DataDir) + " isn't theirs"
	}
	return how
}
