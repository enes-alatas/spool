//go:build integration

package itest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// perfDockerEnv opts into TestPerfSmokeOnDockerWorkstations. It stands up a
// hundred workstations, which takes minutes and a daemon whose address pools
// hold a hundred networks, so it runs from `make perf-docker` and the `perf`
// workflow, not on every `make itest` (#445).
const perfDockerEnv = "SPOOL_PERF_DOCKER"

// TestPerfSmokeOnDockerWorkstations is the perf smoke on docker loops: the
// same envelope and the same four baselines, with every loop in a
// workstation of its own, as ADR-0017 claims they hold. The hub runs at the
// default liveness interval, and the idle window spans two of its sweeps.
// It also logs what the baselines leave out, the workstations' own cost:
// each container's memory and CPU, and the daemon's shim process each one
// keeps.
func TestPerfSmokeOnDockerWorkstations(t *testing.T) {
	if os.Getenv(perfDockerEnv) != "1" {
		t.Skipf("%s=1 runs it: it stands up a hundred workstations (make perf-docker)", perfDockerEnv)
	}
	dataDir := t.TempDir()
	// the hub's default, which is what an operator runs
	const healthInterval = 45 * time.Second
	start := func() *server {
		return startDockerServer(t, dataDir, "--workstation-health-sec", strconv.Itoa(int(healthInterval.Seconds())))
	}
	hub := start()

	awake := make([]string, envelopeAwake)
	asleep := make([]string, envelopeLoops-envelopeAwake)
	for i := range awake {
		awake[i] = fmt.Sprintf("awake-%02d", i)
	}
	for i := range asleep {
		asleep[i] = fmt.Sprintf("asleep-%02d", i)
	}
	ids := map[string]string{}
	for _, name := range slices.Concat(awake, asleep) {
		hub.createLoop(name, nil)
		ids[name] = hub.loop(name).ID
		cleanupWorkstation(t, ids[name])
	}
	// Provisioning a hundred workstations is the slow part, and every
	// creation tick asks for one at once.
	hub.waitFleet(nil, slices.Concat(awake, asleep), 10*time.Minute)

	// A workstation's working directory is out of the test's reach, so an
	// awake loop is scripted through its env: its creation turn has run,
	// the next echoes and leaves it awake, and the one after hangs, which
	// is the turn the boot-recovery phase finds dangling.
	for _, name := range awake {
		hub.setLoopEnv(name, "FAKECLAUDE_SCRIPT", "!echo\n!echo\n!hang 600\n")
		hub.mustJSON("PATCH", "/api/loops/"+name, map[string]any{"idle_timeout_sec": 3600}, nil)
		hub.message(name, "stay awake")
	}
	hub.waitFleet(awake, asleep, 5*time.Minute)

	workstationCost(t, ids, awake)
	measureEnvelope(t, envelope{
		hub: hub, dataDir: dataDir, awake: awake, asleep: asleep,
		idleWindow: 2*healthInterval + 5*time.Second,
		restart:    start,
	})
}

// workstationCost logs what the fleet's workstations cost on top of the
// hub: each container's memory and CPU as docker reports them, split into
// the awake loops' and the asleep ones', and the daemon's shim processes.
func workstationCost(t *testing.T, ids map[string]string, awake []string) {
	t.Helper()
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}").Output()
	if err != nil {
		t.Fatalf("docker stats: %v", err)
	}
	type usage struct{ memMB, cpuPct []float64 }
	byState := map[string]*usage{"awake": {}, "asleep": {}}
	for name, id := range ids {
		state := "asleep"
		if slices.Contains(awake, name) {
			state = "awake"
		}
		row := statsRow(t, string(out), "spool-ws-"+id)
		byState[state].memMB = append(byState[state].memMB, row.memMB)
		byState[state].cpuPct = append(byState[state].cpuPct, row.cpuPct)
	}
	for _, state := range []string{"asleep", "awake"} {
		sample := byState[state]
		t.Logf("%s workstations (%d): memory mean %.1f MB, max %.1f MB; CPU mean %.2f%%, max %.2f%% of one core",
			state, len(sample.memMB), mean(sample.memMB), slices.Max(sample.memMB), mean(sample.cpuPct), slices.Max(sample.cpuPct))
	}
	shims, shimKB := processes(t, "containerd-shim")
	if shims > 0 {
		t.Logf("containerd shims: %d, RSS mean %.1f MB, %.0f MB in all",
			shims, float64(shimKB)/float64(shims)/1024, float64(shimKB)/1024)
	}
}

type statsSample struct{ memMB, cpuPct float64 }

// statsRow reads one container's row out of docker stats' output.
func statsRow(t *testing.T, out, container string) statsSample {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] != container {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64)
		if err != nil {
			t.Fatalf("docker stats CPU %q: %v", fields[1], err)
		}
		used, _, _ := strings.Cut(fields[2], " / ")
		return statsSample{memMB: megabytes(t, used), cpuPct: cpu}
	}
	t.Fatalf("docker stats has no row for %s", container)
	return statsSample{}
}

// megabytes reads a docker size such as "1.25MiB" or "980kB" in MB.
func megabytes(t *testing.T, size string) float64 {
	t.Helper()
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"kB", 1.0 / 1024}, {"B", 1.0 / 1024 / 1024}} {
		if number, ok := strings.CutSuffix(size, unit.suffix); ok {
			value, err := strconv.ParseFloat(number, 64)
			if err != nil {
				t.Fatalf("docker size %q: %v", size, err)
			}
			return value * unit.scale
		}
	}
	t.Fatalf("docker size %q has no unit", size)
	return 0
}

func mean(values []float64) float64 {
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// processes counts the processes whose name starts with prefix, and sums
// their resident sets in kB. One gone between the listing and the read is
// skipped.
func processes(t *testing.T, prefix string) (count int, rssKB int64) {
	t.Helper()
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "status"))
		if err != nil {
			continue
		}
		var name string
		var kb int64
		for _, line := range strings.Split(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "Name:"); ok {
				name = strings.TrimSpace(rest)
			}
			if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				kb, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			}
		}
		if strings.HasPrefix(name, prefix) {
			count++
			rssKB += kb
		}
	}
	return count, rssKB
}
