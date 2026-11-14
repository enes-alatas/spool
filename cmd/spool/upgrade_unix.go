//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// freeBytes is the space a backup can take in dir.
func freeBytes(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

// fileOwner is the uid that owns path.
func fileOwner(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no owner to read", path)
	}
	return int(stat.Uid), nil
}

// openNoFollow opens path for reading, refusing a symlink in its last
// element, and without waiting on a FIFO (#1583: root reads files the
// hub's user wrote). O_NONBLOCK changes nothing for a regular file.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// ownLink is whether info is a file uid owns with no other name: a hard
// link to a file of someone else's is refused.
func ownLink(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid && stat.Nlink == 1
}
