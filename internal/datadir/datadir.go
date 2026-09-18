// Package datadir owns the file permissions on Spool's data directory.
//
// Everything Spool keeps lives under one directory: the database, with bot
// tokens and injected secrets in it; the loop homes, with transcripts and
// checked-out repositories; the worktrees. A default umask creates that
// directory world-readable, which on a shared host means every other local
// account can read a working bot token without going near Spool (#149).
package datadir

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// DirMode and FileMode are what Spool's own data should be: reachable by the
// account that runs the server, by nobody else.
const (
	DirMode  fs.FileMode = 0o700
	FileMode fs.FileMode = 0o600
)

// ownedFiles are the entries directly under the data directory that Spool
// writes and that carry secrets. The two sidecars are the right ones because
// sqlite.Open asks for journal_mode(WAL); a different journal mode would
// leave a different file next to the database, so that pragma and this list
// have to move together.
//
// server.log is the exception: Spool writes to stderr and the operator
// redirects it, so its mode comes from their umask rather than from us — but
// it is where the tokens surfaced in the incident, and covering it costs one
// line here.
var ownedFiles = []string{
	"spool.db",
	"spool.db-wal",
	"spool.db-shm",
	"server.log",
}

// Secure creates the data directory private and narrows it, and the files
// Spool owns inside it, if a previous run or a wider umask left them readable
// to others.
//
// The directory mode is the load-bearing half. Denying traversal to everyone
// else covers what is nested under it — loop homes, transcripts, worktrees —
// without walking a tree that holds whole checkouts on every startup, and
// without fighting the modes git wants inside them.
func Secure(dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	// The directory is the half that has to succeed: if ~/.spool cannot be
	// made private, nothing under it is safe and starting anyway would be a
	// lie about where the secrets are.
	if err := tighten(dir, DirMode, log); err != nil {
		return err
	}
	for _, name := range ownedFiles {
		if err := tighten(filepath.Join(dir, name), FileMode, log); err != nil {
			// A file inside a directory that is already private is reachable
			// by nobody new, so refusing to start over it buys nothing — and
			// server.log is not ours to chmod. One `sudo spool` in a box's
			// history leaves it root-owned; that must not kill every later
			// start.
			log.Warn("could not tighten permissions on spool data", "err", err)
		}
	}
	return nil
}

// tighten clears any permission bit outside want, leaving a mode that is
// already at least as narrow alone. A missing path is not a problem: the file
// is one Spool has not written yet, and it will be created with the right
// mode.
func tighten(path string, want fs.FileMode, log *slog.Logger) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}
	was := info.Mode().Perm()
	narrowed := was & want
	if narrowed == was {
		return nil
	}
	if err := os.Chmod(path, narrowed); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	log.Warn("tightened permissions on spool data",
		"path", path, "was", fmt.Sprintf("%#o", was), "now", fmt.Sprintf("%#o", narrowed))
	return nil
}
