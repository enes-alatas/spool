// Package slack is the Slack implementation of the Surface seam (ADR-0029,
// #230): one Slack app per loop, reached over Socket Mode so a hub needs no
// public URL.
//
// It validates an app's tokens, so a loop can be given one, keeps each such
// loop's Socket Mode connection up, hands the router what a human says to
// the app, and posts what the loop says as the app.
package slack

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/surface/outbound"
)

type Adapter struct {
	store  store.Store
	bus    *bus.Bus
	router *route.Router
	ledger *outbound.Ledger
	client *Client
	log    *slog.Logger
	timing linkTiming

	// ctx is Start's, which every link runs under: a link outlives the
	// request that attached its app. Stop cancels it, and waits for every
	// goroutine counted in running.
	ctx     context.Context
	cancel  context.CancelFunc
	running sync.WaitGroup
	mu      sync.Mutex
	links   map[string]*link // loop ID → its Socket Mode link
	// changing serializes LoopChanged and LoopRemoved, so two edits of one
	// loop cannot both find it unlinked and connect its app twice.
	changing sync.Mutex

	// toldMu guards pairTold and notOwnerTold, which every link's ingest
	// writes: the notices already given this run (notice.go).
	toldMu       sync.Mutex
	pairTold     map[string]bool // Slack user ID
	notOwnerTold map[string]bool // loop ID + ":" + DM channel
	// loginTold holds, for each owner told that a Claude login was
	// refused, the loop whose app told them.
	loginTold map[loginOutage]string
}

// New returns the Slack surface talking to the Web API at apiBase, APIBase
// in production.
func New(st store.Store, publisher *bus.Bus, router *route.Router, log *slog.Logger, apiBase string) *Adapter {
	return &Adapter{store: st, bus: publisher, router: router, client: NewClientAt(apiBase), log: log,
		ledger: &outbound.Ledger{Store: st, Bus: publisher, Log: log, Surface: "slack"},
		timing: defaultLinkTiming, links: map[string]*link{},
		pairTold: map[string]bool{}, notOwnerTold: map[string]bool{}, loginTold: map[loginOutage]string{}}
}

var _ surface.Surface = (*Adapter)(nil)

// Start connects every loop that has a Slack app, and starts carrying their
// sends.
func (adapter *Adapter) Start(ctx context.Context) {
	adapter.mu.Lock()
	adapter.ctx, adapter.cancel = context.WithCancel(ctx)
	ctx = adapter.ctx
	adapter.running.Go(func() { adapter.mirror(ctx) })
	adapter.mu.Unlock()
	loops, err := adapter.store.Loops().List(ctx)
	if err != nil {
		adapter.log.Error("slack: list loops", "err", err)
		return
	}
	for _, loopRecord := range loops {
		if connects(loopRecord) {
			adapter.startLink(loopRecord)
		}
	}
}

// Stop closes every link and the mirror, and waits for them: each link's
// send loop fails what its app still held on the way out.
func (adapter *Adapter) Stop(ctx context.Context) {
	adapter.mu.Lock()
	cancel := adapter.cancel
	if cancel != nil {
		cancel()
	}
	adapter.mu.Unlock()
	if cancel != nil {
		surface.Wait(ctx, &adapter.running)
	}
}

// connects reports whether a loop should hold a Socket Mode connection.
func connects(loopRecord *store.Loop) bool {
	return loopRecord.SlackAppToken != "" && loopRecord.Status != store.StatusArchived
}

// ValidateCredential checks both of an app's tokens before either is stored.
// The bot token must be a bot's: auth.test answers for a user token too,
// and a loop that posted as a person would be a different product. The
// app-level token must open a Socket Mode connection. Asking for the URL
// does not connect to it, and an unused one expires.
func (adapter *Adapter) ValidateCredential(ctx context.Context, credential surface.Credential) (surface.Identity, error) {
	who, err := adapter.client.AuthTest(ctx, credential.Token)
	if err != nil {
		return surface.Identity{}, refusal(surface.PartToken, err)
	}
	if who.BotID == "" {
		return surface.Identity{}, &surface.RejectedError{Part: surface.PartToken,
			Err: errors.New("not a bot token: paste the Bot User OAuth Token (xoxb-…)")}
	}
	if _, err := adapter.client.OpenConnection(ctx, credential.AppToken); err != nil {
		return surface.Identity{}, refusal(surface.PartAppToken, err)
	}
	return surface.Identity{Name: who.User, UserID: who.UserID, TeamID: who.TeamID, TeamName: who.Team}, nil
}

// refusal marks err as a refusal of part when Slack judged the token, and
// passes it through when Slack gave no answer.
func refusal(part string, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Refused() {
		return &surface.RejectedError{Part: part, Err: err}
	}
	return err
}

// LoopChanged brings the loop's link in line with its stored app. Most edits
// are none of Slack's business, and a link that is kept is not reconnected:
// only a different app-level token, or none, changes anything.
func (adapter *Adapter) LoopChanged(ctx context.Context, loopID string) {
	adapter.changing.Lock()
	defer adapter.changing.Unlock()
	loopRecord, err := adapter.store.Loops().Get(ctx, loopID)
	if err != nil {
		adapter.log.Error("slack: read changed loop", "loop", loopID, "err", err)
		return
	}
	if !connects(loopRecord) {
		adapter.stopLink(loopID)
		return
	}
	adapter.mu.Lock()
	current := adapter.links[loopID]
	adapter.mu.Unlock()
	if current != nil && current.credential == loopRecord.SlackAppToken {
		return
	}
	adapter.stopLink(loopID)
	adapter.startLink(loopRecord)
}

func (adapter *Adapter) LoopRemoved(loopID string) {
	adapter.changing.Lock()
	defer adapter.changing.Unlock()
	adapter.stopLink(loopID)
}

// Status reports the Socket Mode link for one loop, in the shape the control
// room renders (#230).
func (adapter *Adapter) Status(loopID string) any {
	adapter.mu.Lock()
	current := adapter.links[loopID]
	adapter.mu.Unlock()
	if current == nil {
		return (&link{}).status()
	}
	return current.status()
}

// startLink connects a loop's app. Before Start there is no context to run
// a link under, and Start connects every loop anyway. After Stop there is
// none either: Stop has cancelled it under this lock, and may already be
// waiting for the last link to close.
func (adapter *Adapter) startLink(loopRecord *store.Loop) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.ctx == nil || adapter.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(adapter.ctx)
	started := &link{loopID: loopRecord.ID, credential: loopRecord.SlackAppToken, cancel: cancel, done: make(chan struct{}),
		sends: make(chan *route.MessagePayload, sends), notices: make(chan notice, notices),
		reactions: make(chan *route.ReactionPayload, reactions), pollEdits: make(chan struct{}, 1)}
	adapter.links[loopRecord.ID] = started
	adapter.running.Go(func() { adapter.run(ctx, started) })
	adapter.running.Go(func() { adapter.nameChannel(ctx, started, loopRecord, loopRecord.SlackChannelID) })
}

// stopLink closes a loop's connection, and waits for it to close: a link
// replaced by another must not still be reading when the new one starts.
func (adapter *Adapter) stopLink(loopID string) {
	adapter.mu.Lock()
	stopped := adapter.links[loopID]
	delete(adapter.links, loopID)
	adapter.mu.Unlock()
	if stopped != nil {
		stopped.cancel()
		<-stopped.done
	}
}
