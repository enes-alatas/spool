//go:build integration

package itest

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloud turn reports no cost, only its tokens: the hub prices them by
// its own table and marks the cost as an estimate, and a model the table
// lacks leaves the cost unknown, never free, with the day's total saying
// it leaves that turn out (ADR-0059).
func TestACloudTurnsCostIsTheHubsEstimate(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")

	// the fake's usage at claude-opus-5-5's prices: 10 input at $4, 5
	// output at $20, 100 cache writes at $5 and 1000 cache reads at $0.20
	// per million tokens
	const estimate = (10*4 + 5*20 + 100*5 + 1000*0.20) / 1e6
	hub.message("cirrus", "priced words")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "priced words") })
	priced := cloudCostTurn(t, hub, "cirrus", "priced words")
	if priced.CostUSD == nil || math.Abs(*priced.CostUSD-estimate) > 1e-12 || !priced.CostEstimated {
		t.Errorf("the priced turn: cost %v, estimated %v; want %v, estimated", priced.CostUSD, priced.CostEstimated, estimate)
	}

	if err := os.WriteFile(filepath.Join(hub.fkState, "cloud-model"), []byte("claude-unpriced-9"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.waitState("cirrus", "asleep", 20*time.Second)
	hub.message("cirrus", "unpriced words")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "unpriced words") })
	unpriced := cloudCostTurn(t, hub, "cirrus", "unpriced words")
	if unpriced.CostUSD != nil || !unpriced.CostEstimated {
		t.Errorf("the unpriced turn: cost %v, estimated %v; want null, estimated", unpriced.CostUSD, unpriced.CostEstimated)
	}

	var viewed struct {
		CostToday    float64 `json:"cost_today_usd"`
		Estimated    bool    `json:"cost_today_estimated"`
		UnknownTurns int     `json:"cost_today_unknown_turns"`
	}
	hub.mustJSON("GET", "/api/loops/cirrus", nil, &viewed)
	if math.Abs(viewed.CostToday-estimate) > 1e-12 || !viewed.Estimated || viewed.UnknownTurns != 1 {
		t.Errorf("the day's cost: %+v, want %v, estimated, leaving one turn out", viewed, estimate)
	}
}

// cloudCostTurnJSON is what the turns route serves of a turn's cost.
type cloudCostTurnJSON struct {
	ResultText    string   `json:"result_text"`
	CostUSD       *float64 `json:"cost_usd"`
	CostEstimated bool     `json:"cost_estimated"`
}

// cloudCostTurn is the loop's turn whose reply holds text.
func cloudCostTurn(t *testing.T, hub *server, name, text string) cloudCostTurnJSON {
	t.Helper()
	var turns []cloudCostTurnJSON
	hub.mustJSON("GET", "/api/loops/"+name+"/turns?limit=50", nil, &turns)
	for _, listed := range turns {
		if strings.Contains(listed.ResultText, text) {
			return listed
		}
	}
	t.Fatalf("no turn of %s replied %q", name, text)
	return cloudCostTurnJSON{}
}
