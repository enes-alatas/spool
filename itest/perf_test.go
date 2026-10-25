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
// process, with the subprocesses it has reaped in its idle CPU: the claude
// processes still running, fakeclaude's here, are the loops' cost, not
// Spool's. The loops are bare; TestPerfSmokeOnDockerWorkstations
// measures the same on docker ones.
func TestPerfSmokeAtTheScaleEnvelope(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("the perf smoke reads the hub's CPU and memory from /proc")
	}
	t.Parallel()
	dataDir := t.TempDir()
	hub := startServer(t, dataDir)

	// The awake loops answer their creation tick and then hang on every
	// later turn. They stay awake between turns, and their hung turns are
	// what the boot-recovery phase finds dangling.
	awake := make([]string, envelopeAwake)
	for i := range awake {
		awake[i] = fmt.Sprintf("awake-%02d", i)
		hub.createLoop(awake[i], map[string]any{
			"workspace_path":   workspaceWithScript(t, "!echo\n!hang 600\n"),
			"workspace_mode":   "dir",
			"idle_timeout_sec": 3600,
		})
	}
	asleep := make([]string, envelopeLoops-envelopeAwake)
	for i := range asleep {
		asleep[i] = fmt.Sprintf("asleep-%02d", i)
		hub.createLoop(asleep[i], nil)
	}
	// every loop's creation tick wakes it once; the envelope is only
	// standing once those turns are done and the fleet has settled
	hub.waitFleet(awake, asleep, 3*time.Minute)

	measureEnvelope(t, envelope{
		hub: hub, dataDir: dataDir, awake: awake, asleep: asleep,
		idleWindow: idleWindow,
		restart:    func() *server { return startServer(t, dataDir) },
	})
}

// envelope is a fleet standing at the scale envelope and settled: every
// awake loop idle, with a hang as its next turn, and every asleep one
// asleep.
type envelope struct {
	hub           *server
	dataDir       string
	awake, asleep []string
	// idleWindow is how long the idle phase samples the hub's CPU for.
	idleWindow time.Duration
	// restart starts a hub on dataDir again, the way the fleet's was.
	restart func() *server
}

// measureEnvelope asserts the four orchestrator baselines on a standing
// envelope.
func measureEnvelope(t *testing.T, fleet envelope) {
	t.Helper()
	hub := fleet.hub
	t.Run("idle", func(t *testing.T) {
		time.Sleep(time.Second) // let the last exits and writes land
		before := hubCPU(t, hub)
		time.Sleep(fleet.idleWindow)
		after := hubCPU(t, hub)
		used := float64(after.own-before.own) / clockTicks / fleet.idleWindow.Seconds()
		// The hub's subprocesses are part of what it costs to stand idle: on
		// a docker fleet, the docker CLI calls of the liveness sweep.
		children := float64(after.children-before.children) / clockTicks / fleet.idleWindow.Seconds()
		rss := hubRSSKB(t, hub)
		t.Logf("idle at the envelope: CPU %.2f%% of one core over %s, its subprocesses %.2f%% more, RSS %.1f MB",
			used*100, fleet.idleWindow, children*100, float64(rss)/1024)
		if used+children > idleCPUBudget {
			t.Errorf("idle CPU %.2f%% of one core with its subprocesses, want ~0 (under %.0f%%)",
				(used+children)*100, idleCPUBudget*100)
		}
		if rss > idleRSSBudgetKB {
			t.Errorf("idle RSS %.1f MB, want under %d MB", float64(rss)/1024, idleRSSBudgetKB/1024)
		}
	})

	t.Run("wake", func(t *testing.T) {
		var overheads []time.Duration
		for burst := range wakeBursts {
			targets := fleet.asleep[burst*envelopeAwake : (burst+1)*envelopeAwake]
			sentAt := map[string]int64{}
			for _, name := range targets {
				sentAt[name] = time.Now().UnixMilli()
				hub.message(name, "wake up")
			}
			for _, name := range targets {
				woke := hub.waitTurn(name, time.Minute, func(tr turn) bool {
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
		for _, name := range fleet.awake {
			hub.message(name, "hang")
		}
		for _, name := range fleet.awake {
			hub.waitRunningTurn(name, time.Minute, func(tr turn) bool { return tr.Trigger == "message" })
		}
		if err := hub.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		<-hub.exited
		overdueEveryTick(t, fleet.dataDir, time.Now().Add(-time.Hour).UnixMilli())

		// startServer returns on the first healthy answer, and the listeners
		// are served only once the boot has interrupted the dangling turns,
		// reaped the stored pids and respread the overdue ticks (#595). The
		// time includes the process start and the harness's 100ms health
		// poll.
		started := time.Now()
		restarted := fleet.restart()
		took := time.Since(started)
		t.Logf("boot recovery at the envelope: %s", took)
		if took > bootRecoveryLimit {
			t.Errorf("boot recovery took %s, want under %s", took, bootRecoveryLimit)
		}
		for _, name := range fleet.awake {
			for _, tr := range restarted.turns(name) {
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

// cpuTicks is CPU time, user and system together, in clock ticks.
type cpuTicks struct {
	// own is the hub process's.
	own int64
	// children is its subprocesses', counted as the hub reaps each: one
	// still running is not in it yet.
	children int64
}

// hubCPU is the CPU time the hub process and its subprocesses have used.
func hubCPU(t *testing.T, hub *server) cpuTicks {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", hub.cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	// the command name is parenthesised and may hold spaces; the fields
	// counted from after it are utime, stime, cutime and cstime at 12 to 15
	fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
	var ticks [4]int64
	for i, field := range fields[11:15] {
		count, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			t.Fatalf("parse /proc stat: %v", err)
		}
		ticks[i] = count
	}
	return cpuTicks{own: ticks[0] + ticks[1], children: ticks[2] + ticks[3]}
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
