package httpapi

import (
	"testing"

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
			got := planUsageOf(test.record, test.now)
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
