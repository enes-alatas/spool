package httpapi

import (
	"testing"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

func TestPlanUsageOf(t *testing.T) {
	observed := store.PlanUsageRecord{
		FiveHour: &store.PlanUsageWindow{Utilization: 0.42, ResetsAt: 5000},
		SevenDay: &store.PlanUsageWindow{Utilization: 1.25, ResetsAt: 90000},
		At:       2000,
		Source:   "stream",
	}
	tests := []struct {
		name   string
		record store.PlanUsageRecord
		now    int64
		want   planUsageView
	}{
		{
			name:   "nothing observed reads unknown with the reason",
			record: store.PlanUsageRecord{},
			now:    3000,
			want:   planUsageView{Unknown: unknownNotObserved},
		},
		{
			name:   "both windows read as percent, past 100 kept",
			record: observed,
			now:    3000,
			want: planUsageView{
				FiveHour: &planWindowView{UsedPercent: 42, ResetsAt: 5000},
				SevenDay: &planWindowView{UsedPercent: 125, ResetsAt: 90000},
				AsOf:     2000, Source: "stream",
			},
		},
		{
			name:   "a window past its reset reads reset, not its stale percent",
			record: observed,
			now:    5000,
			want: planUsageView{
				FiveHour: &planWindowView{Reset: true},
				SevenDay: &planWindowView{UsedPercent: 125, ResetsAt: 90000},
				AsOf:     2000, Source: "stream",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := planUsageOf(test.record, loop.Cap{}, 0, 0, test.now)
			if !sameWindow(got.FiveHour, test.want.FiveHour) || !sameWindow(got.SevenDay, test.want.SevenDay) ||
				got.AsOf != test.want.AsOf || got.Source != test.want.Source || got.Unknown != test.want.Unknown {
				t.Fatalf("planUsageOf = %+v %+v %+v\nwant %+v %+v %+v", got, got.FiveHour, got.SevenDay, test.want, test.want.FiveHour, test.want.SevenDay)
			}
		})
	}
}

func sameWindow(got, want *planWindowView) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

// TestPlanUsageOfCap: each window carries its threshold, a holding cap is
// reported with its windows and wake time, and a Resume now replaces it
// with the hold's end.
func TestPlanUsageOfCap(t *testing.T) {
	record := store.PlanUsageRecord{
		FiveHour: &store.PlanUsageWindow{Utilization: 0.95, ResetsAt: 5000},
		SevenDay: &store.PlanUsageWindow{Utilization: 0.3, ResetsAt: 90000},
		At:       2000, Source: "stream",
	}
	capped := loop.Cap{Windows: []string{loop.PlanWindowFiveHour}, Until: 5000}
	got := planUsageOf(record, capped, 90, 0, 3000)
	if got.FiveHour.CapPercent != 90 || got.SevenDay.CapPercent != 0 {
		t.Fatalf("cap_percent = %d, %d; want 90, 0", got.FiveHour.CapPercent, got.SevenDay.CapPercent)
	}
	if got.Cap == nil || got.Cap.Until != 5000 || len(got.Cap.Windows) != 1 || got.ResumedUntil != 0 {
		t.Fatalf("a holding cap reads cap %+v, resumed_until %d", got.Cap, got.ResumedUntil)
	}

	capped.ResumedUntil = 5000
	got = planUsageOf(record, capped, 90, 0, 3000)
	if got.Cap != nil || got.ResumedUntil != 5000 {
		t.Fatalf("a resumed cap reads cap %+v, resumed_until %d; want null and 5000", got.Cap, got.ResumedUntil)
	}
}
