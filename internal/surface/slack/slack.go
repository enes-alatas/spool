// Package slack is the Slack implementation of the Surface seam (ADR-0029,
// #230): one Slack app per loop, reached over Socket Mode so a hub needs no
// public URL.
//
// This first cut validates an app's tokens, so a loop can be given one.
// It does not connect yet: the Socket Mode connection, ingest and mirrors
// are the next slice of #230, and Status says so rather than pretending.
package slack

import (
	"context"
	"errors"
	"log/slog"

	"github.com/enes-alatas/spool/internal/surface"
)

type Adapter struct {
	client *Client
	log    *slog.Logger
}

// New returns the Slack surface talking to the Web API at apiBase, APIBase
// in production.
func New(log *slog.Logger, apiBase string) *Adapter {
	return &Adapter{client: NewClientAt(apiBase), log: log}
}

var _ surface.Surface = (*Adapter)(nil)

// Start has nothing to bring up until the Socket Mode connection lands.
func (a *Adapter) Start(ctx context.Context) {}

// ValidateCredential checks both of an app's tokens before either is stored.
// The bot token must be a bot's: auth.test answers for a user token too,
// and a loop that posted as a person would be a different product. The
// app-level token must open a Socket Mode connection. Asking for the URL
// does not connect to it, and an unused one expires.
func (a *Adapter) ValidateCredential(ctx context.Context, credential surface.Credential) (surface.Identity, error) {
	who, err := a.client.AuthTest(ctx, credential.Token)
	if err != nil {
		return surface.Identity{}, refusal(surface.PartToken, err)
	}
	if who.BotID == "" {
		return surface.Identity{}, &surface.RejectedError{Part: surface.PartToken,
			Err: errors.New("not a bot token: paste the Bot User OAuth Token (xoxb-…)")}
	}
	if _, err := a.client.OpenConnection(ctx, credential.AppToken); err != nil {
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

func (a *Adapter) LoopChanged(ctx context.Context, loopID string) {}

func (a *Adapter) LoopRemoved(loopID string) {}

// Status reports the Socket Mode link for one loop, in the shape the control
// room renders (#230). Nothing connects yet, so every loop reads as not
// connected, which is true.
func (a *Adapter) Status(loopID string) any {
	return map[string]any{
		"connected":      false,
		"last_event_at":  int64(0),
		"last_error":     "",
		"ignored_events": 0,
	}
}
