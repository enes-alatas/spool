package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
)

// Health reports a workstation's liveness from a short-TTL cache filled by
// one batched docker ps sweep, so per-loop polling stays free of subprocess
// churn and the idle-CPU baseline holds at fleet scale (QUALITY.md,
// ADR-0018).
func (rt *Runtime) Health(ctx context.Context, loopID string) (runtime.Health, error) {
	fleet, err := rt.fleetHealth(ctx)
	if err != nil {
		return runtime.Health{Up: false, Detail: "docker daemon unreachable"}, nil
	}
	if health, ok := fleet[loopID]; ok {
		return health, nil
	}
	return runtime.Health{Up: false, Detail: "workstation not found"}, nil
}

func (rt *Runtime) fleetHealth(ctx context.Context) (map[string]runtime.Health, error) {
	rt.healthMu.Lock()
	defer rt.healthMu.Unlock()
	if time.Since(rt.healthAt) < rt.healthTTL {
		return rt.fleet, rt.fleetErr
	}
	out, err := rt.command(ctx, queryTimeout,
		"ps", "--all", "--filter", "label=spool.loop.id", "--format", "{{json .}}")
	rt.healthAt = time.Now()
	if err != nil {
		rt.fleet, rt.fleetErr = nil, err
		return nil, err
	}
	rt.fleet, rt.fleetErr = parseFleet(out), nil
	return rt.fleet, nil
}

// parseFleet maps loop ids to health from docker ps --format '{{json .}}'
// output, one JSON object per line. Detail carries docker's human status
// ("Exited (137) 3 minutes ago") only when the workstation is down.
func parseFleet(out []byte) map[string]runtime.Health {
	fleet := map[string]runtime.Health{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			Labels string `json:"Labels"`
			State  string `json:"State"`
			Status string `json:"Status"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		loopID := labelValue(row.Labels, "spool.loop.id")
		if loopID == "" {
			continue
		}
		health := runtime.Health{Up: row.State == "running"}
		if !health.Up {
			health.Detail = row.Status
		}
		fleet[loopID] = health
	}
	return fleet
}

// labelValue pulls one key out of docker ps's comma-joined "k=v,k=v" label
// rendering.
func labelValue(labels, key string) string {
	for _, pair := range strings.Split(labels, ",") {
		if value, found := strings.CutPrefix(pair, key+"="); found {
			return value
		}
	}
	return ""
}
