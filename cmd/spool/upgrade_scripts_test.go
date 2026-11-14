package main

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Run as root under a unit, the upgrade runs the backup and the restore as
// the unit's User=, never as root, and keeps the backup as its own, mode
// 0600, in --backup-dir, with no directory it handed over left (#1583).
func TestUpgradeRunsTheScriptsAsTheUnitsUser(t *testing.T) {
	env, up, host, out := newUpgradeEnv(t)
	me, _ := user.Current()
	env.startsAs = "v0.5.1"
	host.answer = func(cmd hostCmd) (string, error, bool) {
		if cmd.Name == env.backupPath || cmd.Name == env.restorePath {
			if !slices.Equal(cmd.Env, []string{scriptPATH, "HOME=" + me.HomeDir}) {
				t.Errorf("%s runs with %q; want PATH and HOME alone", cmd.Name, cmd.Env)
			}
		}
		return "", nil, false
	}
	if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("upgrade = %v; want a rollback\n%s", err, out)
	}
	if want := []string{"backup as " + me.Username, "restore as " + me.Username}; !slices.Equal(env.scriptUsers, want) {
		t.Errorf("the scripts ran %q; want %q", env.scriptUsers, want)
	}
	for _, name := range []string{"spool-data-20261016T000000Z.tar.gz", "spool-keys-20261016T000000Z.tar"} {
		info, err := os.Lstat(filepath.Join(env.backupDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("%s in %s: %v, %v; want a regular file of mode 0600", name, env.backupDir, info, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(env.backupDir), ".spool-upgrade-*")); len(left) != 0 {
		t.Errorf("handed-over directories left: %q", left)
	}
}

// A unit with no User= runs the hub as root, and its scripts as root, as
// before #1583; so does a restore whose data directory's parent isn't the
// unit user's, as the restore makes the directory there.
func TestUpgradeRunsTheScriptsAsRootWhereItMust(t *testing.T) {
	t.Run("no User=", func(t *testing.T) {
		env, up, _, out := newUpgradeEnv(t)
		env.unitUser, env.startsAs = "", "v0.5.1"
		if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "rolled back") {
			t.Fatalf("upgrade = %v; want a rollback\n%s", err, out)
		}
		if want := []string{"backup as ", "restore as "}; !slices.Equal(env.scriptUsers, want) {
			t.Errorf("the scripts ran %q; want %q", env.scriptUsers, want)
		}
	})
	t.Run("a parent not the user's", func(t *testing.T) {
		env, up, _, out := newUpgradeEnv(t)
		env.startsAs = "v0.5.1"
		me, _ := user.Current()
		up.ownerOf = func(path string) (*user.User, error) {
			if path == filepath.Dir(env.dataDir) {
				return &user.User{Uid: "0", Gid: "0", Username: "root"}, nil
			}
			return me, nil
		}
		if err := up.run(context.Background()); err == nil || !strings.Contains(err.Error(), "rolled back") {
			t.Fatalf("upgrade = %v; want a rollback\n%s", err, out)
		}
		if want := []string{"backup as " + me.Username, "restore as "}; !slices.Equal(env.scriptUsers, want) {
			t.Errorf("the scripts ran %q; want %q", env.scriptUsers, want)
		}
	})
}

// A User= that names no user here refuses before anything stops.
func TestUpgradeRefusesAnUnknownUnitUser(t *testing.T) {
	env, up, host, _ := newUpgradeEnv(t)
	env.unitUser = "nosuchuser"
	err := up.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "User=nosuchuser") || stopped(host) {
		t.Errorf("upgrade = %v, stopped %v; want a refusal naming User=nosuchuser before the stop", err, stopped(host))
	}
}

// The upgrade takes in only a regular file the backup wrote in the
// directory it handed over: not a symlink, and nothing outside it.
func TestTakeInRefusesWhatTheUserCouldPointElsewhere(t *testing.T) {
	root := t.TempDir()
	stage, dir := filepath.Join(root, "stage"), filepath.Join(root, "backups")
	for _, d := range []string{stage, dir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(root, "secret")
	write(t, secret, "not the backup")
	if err := os.Symlink(secret, filepath.Join(stage, "spool-data-1.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, "spool-keys-1.tar"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stage, "spool-data-2.tar.gz"), "data")
	for path, want := range map[string]string{
		filepath.Join(stage, "spool-data-1.tar.gz"): "symbolic link",
		filepath.Join(stage, "spool-keys-1.tar"):    "not a regular file",
		secret:                                      "not in",
		stage + "/../secret":                        "not in",
	} {
		if _, err := takeIn(stage, path, dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("takeIn %s = %v; want a refusal (%s)", path, err, want)
		}
	}
	got, err := takeIn(stage, filepath.Join(stage, "spool-data-2.tar.gz"), dir)
	if err != nil || got != filepath.Join(dir, "spool-data-2.tar.gz") || read(t, got) != "data" {
		t.Errorf("takeIn = %s, %v; want the file copied into %s", got, err, dir)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%s holds %d entries; want only the one taken in", dir, len(entries))
	}
}
