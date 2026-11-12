package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// stalledJoiner is a surface whose platform never answers a join offer:
// each ask waits until its context ends.
type stalledJoiner struct {
	surface.Surface
}

func (stalledJoiner) JoinOffer(ctx context.Context, _, _ string) (surface.JoinOffer, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (stalledJoiner) JoinChat(context.Context, string, string) error { return nil }

// A platform that doesn't answer holds a channel's view joinWait in all,
// not joinWait for each member it asks about, and leaves their join out.
func TestJoinOffersShareOneWait(t *testing.T) {
	saved := joinWait
	joinWait = 200 * time.Millisecond
	t.Cleanup(func() { joinWait = saved })
	server := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Surfaces: map[string]surface.Surface{store.SurfaceSlack: stalledJoiner{}}}
	members := []*store.Loop{{ID: "l1", Name: "milo"}, {ID: "l2", Name: "terra"}, {ID: "l3", Name: "iris"}}
	view := &chatView{Members: []chatMemberView{
		{Loop: "milo", Status: chatStatusNotInChat},
		{Loop: "terra", Status: chatStatusNotInChat},
		{Loop: "iris", Status: chatStatusCarrying},
	}}

	start := time.Now()
	server.fillJoinOffers(context.Background(), view, members, &store.ChannelChat{Surface: store.SurfaceSlack, ChatID: "C0BACKEND"})
	if took := time.Since(start); took >= 2*joinWait {
		t.Fatalf("two stalled offers took %v, want under %v: they were asked one after another", took, 2*joinWait)
	}
	for _, place := range view.Members {
		if place.Join != "" {
			t.Errorf("%s's join = %q, want none when the platform didn't answer", place.Loop, place.Join)
		}
	}
}

// slowJoiner is a surface whose platform answers each join offer after a
// while, so offers asked one after another run out of the shared wait.
type slowJoiner struct {
	surface.Surface
	after time.Duration
}

func (joiner slowJoiner) JoinOffer(ctx context.Context, _, _ string) (surface.JoinOffer, error) {
	select {
	case <-time.After(joiner.after):
		return surface.JoinAvailable, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (slowJoiner) JoinChat(context.Context, string, string) error { return nil }

// Offers are asked at once: two answers that each take most of joinWait
// both arrive within it.
func TestJoinOffersAreAskedAtOnce(t *testing.T) {
	saved := joinWait
	joinWait = 400 * time.Millisecond
	t.Cleanup(func() { joinWait = saved })
	server := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Surfaces: map[string]surface.Surface{store.SurfaceSlack: slowJoiner{after: 250 * time.Millisecond}}}
	members := []*store.Loop{{ID: "l1", Name: "milo"}, {ID: "l2", Name: "terra"}}
	view := &chatView{Members: []chatMemberView{
		{Loop: "milo", Status: chatStatusNotInChat},
		{Loop: "terra", Status: chatStatusNotInChat},
	}}
	server.fillJoinOffers(context.Background(), view, members, &store.ChannelChat{Surface: store.SurfaceSlack, ChatID: "C0BACKEND"})
	for _, place := range view.Members {
		if place.Join != surface.JoinAvailable {
			t.Errorf("%s's join = %q, want available: the offers were asked one after another", place.Loop, place.Join)
		}
	}
}
