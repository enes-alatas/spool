package hook

import (
	"strings"
	"testing"
)

// TestKillsByNameAreRefused: the kills that reach past the loop's own
// processes without pkill, and the next obvious phrasings (#413).
func TestKillsByNameAreRefused(t *testing.T) {
	for _, command := range []string{
		// PID -1: every process of the uid
		"kill -9 -1",
		"kill -KILL -1",
		"kill -SIGTERM -1",
		"kill -s KILL -1",
		"kill -n 9 -1",
		"kill -- -1",
		"kill -9 -- -1",
		"kill -9 4242 -1",
		"/bin/kill -9 -1",
		"sudo kill -9 -1",
		"make itest; kill -9 -1",
		// a PID looked up by name
		"kill $(pgrep -f 'go test')",
		"kill -9 $(pgrep fakeclaude)",
		"kill `pidof fakeclaude`",
		"kill \"$(pgrep -f spool)\"",
		"pgrep -f 'go test' | xargs kill",
		"pgrep -f 'go test' | xargs -r kill -9",
		"pgrep -u loop claude | xargs kill",
		"for pid in $(pgrep -f itest); do kill $pid; done",
		"pids=$(pgrep -f itest); kill $pids",
		"pidof spool | xargs kill",
		"pgrep -P 1 -f 'go test' | xargs kill",
		"pgrep -P1 -f itest | xargs kill",
		"kill $(pgrep --parent=1 fakeclaude)",
		"kill $(pgrep --parent 1 fakeclaude)",
		"pgrep -P 1,4242 | xargs kill",
		"pgrep -P 4242 -P 1 | xargs kill",
		// a PID looked up by port or file
		"kill $(lsof -t -i :8080)",
		"kill -9 $(lsof -ti :8080)",
		"lsof -ti tcp:8080 | xargs kill",
		"lsof -t spool.db | xargs -r kill -9",
		"fuser -k 8080/tcp",
		"fuser -k -n tcp 8080",
		"fuser -km /srv/data",
		"fuser --kill 8080/tcp",
		"sudo fuser -k 8080/tcp",
		"sh -c 'kill $(pgrep -f x)'",
		// every process outside the session
		"killall5 -9",
		"sudo killall5",
	} {
		reason := Check(bash(command))
		if reason == "" {
			t.Errorf("let through: %s", command)
			continue
		}
		if !strings.Contains(reason, "$!") {
			t.Errorf("the reason for %q does not say how to kill by PID: %s", command, reason)
		}
	}
}

// TestKillsByPIDRun: a kill aimed at a PID the loop knows, and lookups
// that kill nothing.
func TestKillsByPIDRun(t *testing.T) {
	for _, command := range []string{
		"kill 4242",
		"kill -9 4242",
		"kill -1 4242",
		"kill -s HUP 4242",
		"kill -TERM $(cat run.pid)",
		"kill $!",
		"kill %1",
		"kill -- -4242",
		"kill -l",
		"kill -1",
		"pgrep -f 'go test'",
		"pgrep -P $$ | xargs kill",
		"kill $(pgrep -P 4242)",
		"pgrep --parent=4242 | xargs -r kill",
		"kill $(pgrep -P 4242,4243)",
		"lsof -t -i :8080",
		"lsof -i :8080 && kill 4242",
		"fuser -v 8080/tcp",
		"fuser 8080/tcp",
		"pidof spool",
		"echo 'never kill -9 -1'",
		"git commit -m 'fix: refuse kill $(pgrep x)'",
	} {
		if reason := Check(bash(command)); reason != "" {
			t.Errorf("refused %q: %s", command, reason)
		}
	}
}
