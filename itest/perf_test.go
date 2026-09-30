//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// The scale envelope and the four baselines QUALITY.md holds the
// orchestrator to there (ADR-0013). A number changes by ADR, not here.
const (
	envelopeLoops = 100
	envelopeAwake = 15

	idleCPUBudget     = 0.01 // of one core: "~0%", no busy polling
	idleRSSBudgetKB   = 100 * 1024
	wakeOverheadP95   = 500 * time.Millisecond
	bootRecoveryLimit = 3 * time.Second

	// wakeBursts of envelopeAwake asleep loops are messaged at once, as a
	// message to every loop in the fleet channel wakes them: wakes that
	// queue behind each other are what a lone wake cannot show. Three
	// bursts, so a p95 is a p95 of something.
	wakeBursts = 3
	// idleWindow is how long the hub's CPU time is sampled for. The kernel
	// counts it in clock ticks, 10ms each, so a window this long reads a
	// 1% budget as 50 ticks: coarse enough not to flake, fine enough to
	// catch a loop that spins.
	idleWindow = 5 * time.Second
	// clockTicks is USER_HZ, which Linux fixes at 100 for /proc.
	clockTicks = 100
)

// TestPerfSmokeAtTheScaleEnvelope builds the envelope, 100 defined loops
// with 15 awake, and asserts QUALITY.md's four orchestrator baselines on it:
// idle CPU and RSS, wake overhead, and boot recovery. It measures the hub
// process alone: the claude processes, fakeclaude's here, are the loops'
// cost, not Spool's.
func TestPerfSmokeAtTheScaleEnvelope(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("the perf smoke reads the hub's CPU and memory from /proc")
	}
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)

	// The awake loops answer their creation tick and then hang on every
	// later turn. They stay awake between turns, and their hung turns are
	// what the boot-recovery phase finds dangling.
	awake := make([]string, envelopeAwake)
	for i := range awake {
		awake[i] = fmt.Sprintf("awake-%02d", i)
		s.createLoop(awake[i], map[string]any{
			"workspace_path":   workspaceWithScript(t, "!echo\n!hang 600\n"),
			"workspace_mode":   "dir",
			"idle_timeout_sec": 3600,
		})
	}
	asleep := make([]string, envelopeLoops-envelopeAwake)
	for i := range asleep {
		asleep[i] = fmt.Sprintf("asleep-%02d", i)
		s.createLoop(asleep[i], nil)
	}
	// every loop's creation tick wakes it once; the envelope is only
	// standing once those turns are done and the fleet has settled
	s.waitFleet(awake, asleep, 3*time.Minute)

	t.Run("idle", func(t *testing.T) {
		time.Sleep(time.Second) // let the last exits and writes land
		before := hubCPUTicks(t, s)
		time.Sleep(idleWindow)
		used := float64(hubCPUTicks(t, s)-before) / clockTicks / idleWindow.Seconds()
		rss := hubRSSKB(t, s)
		t.Logf("idle at the envelope: CPU %.2f%% of one core over %s, RSS %.1f MB",
			used*100, idleWindow, float64(rss)/1024)
		if used > idleCPUBudget {
			t.Errorf("idle CPU %.2f%% of one core, want ~0 (under %.0f%%)", used*100, idleCPUBudget*100)
		}
		if rss > idleRSSBudgetKB {
			t.Errorf("idle RSS %.1f MB, want under %d MB", float64(rss)/1024, idleRSSBudgetKB/1024)
		}
	})

	t.Run("wake", func(t *testing.T) {
		var overheads []time.Duration
		for burst := range wakeBursts {
			targets := asleep[burst*envelopeAwake : (burst+1)*envelopeAwake]
			sentAt := map[string]int64{}
			for _, name := range targets {
				sentAt[name] = time.Now().UnixMilli()
				s.message(name, "wake up")
			}
			for _, name := range targets {
				woke := s.waitTurn(name, time.Minute, func(tr turn) bool {
					return tr.Trigger == "message"
				})
				overheads = append(overheads, time.Duration(woke.StartedAt-sentAt[name])*time.Millisecond)
			}
		}
		slices.Sort(overheads)
		p95 := overheads[(len(overheads)*95+99)/100-1]
		t.Logf("wake overhead over %d wakes: p50 %s, p95 %s, max %s",
			len(overheads), overheads[len(overheads)/2], p95, overheads[len(overheads)-1])
		if p95 > wakeOverheadP95 {
			t.Errorf("wake overhead p95 %s, want under %s", p95, wakeOverheadP95)
		}
	})

	t.Run("boot recovery", func(t *testing.T) {
		// Crash the hub with every awake loop mid-turn. Its turns are left
		// dangling and its pid stored, and while it is down every tick falls
		// overdue: all three things a boot recovers.
		for _, name := range awake {
			s.message(name, "hang")
		}
		for _, name := range awake {
			s.waitRunningTurn(name, time.Minute, func(tr turn) bool { return tr.Trigger == "message" })
		}
		if err := s.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		<-s.exited
		overdueEveryTick(t, dataDir, time.Now().Add(-time.Hour).UnixMilli())

		// startServer returns on the first healthy answer, and the listeners
		// are served only once the boot has interrupted the dangling turns
		// and reaped the stored pids. The overdue ticks are respread by the
		// scheduler as it starts, beside the listeners; the time includes
		// the process start and the harness's 100ms health poll.
		started := time.Now()
		s2 := startServer(t, dataDir)
		took := time.Since(started)
		t.Logf("boot recovery at the envelope: %s", took)
		if took > bootRecoveryLimit {
			t.Errorf("boot recovery took %s, want under %s", took, bootRecoveryLimit)
		}
		for _, name := range awake {
			for _, tr := range s2.turns(name) {
				if tr.EndedAt == 0 {
					t.Fatalf("%s: turn %s still dangling after the boot", name, tr.ID)
				}
			}
		}
	})
}

// waitFleet waits until every awake loop is idle and every asleep one is
// asleep, reading the whole fleet in one request per poll.
func (s *server) waitFleet(awake, asleep []string, timeout time.Duration) {
	s.t.Helper()
	want := map[string]string{}
	for _, name := range awake {
		want[name] = "idle"
	}
	for _, name := range asleep {
		want[name] = "asleep"
	}
	deadline := time.Now().Add(timeout)
	for {
		var fleet []loopView
		s.mustJSON("GET", "/api/loops", nil, &fleet)
		var off []string
		for _, loop := range fleet {
			if state, ok := want[loop.Name]; ok && loop.State != state {
				off = append(off, loop.Name+"="+loop.State)
			}
		}
		if len(fleet) == len(want) && len(off) == 0 {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the fleet never settled within %s: %d loops, off: %v", timeout, len(fleet), off)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// hubCPUTicks is the CPU time the hub process has used, user and system, in
// clock ticks.
func hubCPUTicks(t *testing.T, s *server) int64 {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", s.cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	// the command name is parenthesised and may hold spaces; the fields
	// counted from after it are utime and stime at 12 and 13
	fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
	var ticks int64
	for _, field := range fields[11:13] {
		n, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			t.Fatalf("parse /proc stat: %v", err)
		}
		ticks += n
	}
	return ticks
}

// hubRSSKB is the hub process's resident set, in kB.
func hubRSSKB(t *testing.T, s *server) int64 {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", s.cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				t.Fatalf("parse VmRSS %q: %v", line, err)
			}
			return kb
		}
	}
	t.Fatal("no VmRSS in /proc status")
	return 0
}

// overdueEveryTick makes every loop's tick due at, in spool.db, which is
// how a test stands in for a hub that was down while they fell due. The hub
// must be stopped.
func overdueEveryTick(t *testing.T, dataDir string, at int64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Exec(`UPDATE schedule SET next_tick_at=?`, at)
	if err != nil {
		t.Fatalf("overdue ticks: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != envelopeLoops {
		t.Fatalf("overdue ticks touched %d schedule rows, want %d (%v)", n, envelopeLoops, err)
	}
}
