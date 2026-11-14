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
// element (#1583: root reads files the hub's user wrote).
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
