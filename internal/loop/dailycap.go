package loop

import (
	"context"
	"time"
)

// LocalDay is the calendar day now falls on, in now's own location: its
// first instant and the next day's. A day is built from the date rather
// than a duration, so it is 23 or 25 hours long across a DST change.
func LocalDay(now time.Time) (start, next time.Time) {
	year, month, day := now.Date()
	start = time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	return start, time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
}

// dailyCapUntil reports when the loop's own daily USD cap lets it go: the
// start of the hub's next local day, while the loop's spend today is at or
// over its cap, and 0 while the cap doesn't hold (#652). The spend is the
// sum of its turns' cost_usd since the day started, read again after each
// turn that recorded one and when the day turns. A cloud loop's is the
// hub's estimate, and a turn whose cost is unknown adds nothing. A spend
// that can't be read caps nothing, as an unknown plan usage doesn't.
func (actor *Actor) dailyCapUntil() int64 {
	if actor.loop.DailyCapUSD <= 0 {
		return 0
	}
	start, next := LocalDay(time.Now())
	if actor.spendStale || actor.spendDay != start.UnixMilli() {
		spend, err := actor.deps.Store.Turns().CostSince(context.Background(), actor.loop.ID, start.UnixMilli())
		if err != nil {
			actor.log().Error("daily cap: spend unreadable", "err", err)
			return 0
		}
		actor.spendToday, actor.spendDay, actor.spendStale = spend.USD, start.UnixMilli(), false
	}
	if actor.spendToday < actor.loop.DailyCapUSD {
		return 0
	}
	return next.UnixMilli()
}

// armDailyCap sets the timer that wakes the loop when its daily cap lets
// it go, if the cap holds now. It is called when what the cap rests on may
// have changed: a turn's cost recorded, the cap edited, the actor started.
// A cap that took hold at a turn's end is marked as one the loop is under,
// so the day's turn reads as a lift and wakes it (capChanged).
func (actor *Actor) armDailyCap() {
	if actor.dailyCapTimer == nil {
		return // an actor built without its run loop, as a unit test's is
	}
	actor.dailyCapTimer.Stop()
	until := actor.dailyCapUntil()
	if until == 0 {
		return
	}
	actor.wasCapped = true
	actor.dailyCapTimer.Reset(time.Duration(until-now()) * time.Millisecond)
}
