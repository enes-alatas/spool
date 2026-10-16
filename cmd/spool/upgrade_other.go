//go:build !unix

package main

import "errors"

var errUpgradeUnix = errors.New("spool upgrade runs on a systemd host only")

func freeBytes(string) (uint64, error) { return 0, errUpgradeUnix }

func fileOwner(string) (int, error) { return 0, errUpgradeUnix }
