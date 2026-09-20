package redact

import (
	"context"
	"errors"

	"github.com/enes-alatas/spool/internal/store"
)

// StoreSource reads every secret Spool holds out of the store: the
// operator's Claude token, and per loop its bot token, its hub MCP token and
// its tool secrets.
//
// It reads through the *undecorated* store. Nothing here is persisted, so
// there is nothing to redact, and a redactor asking a redacted store for the
// values it redacts by is a loop worth not building.
type StoreSource struct{ Store store.Store }

func (s StoreSource) Secrets(ctx context.Context) ([]Secret, error) {
	var out []Secret

	token, err := s.Store.Settings().Get(ctx, store.SettingClaudeOAuthToken)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if token != "" {
		out = append(out, Secret{Name: store.SettingClaudeOAuthToken, Value: token})
	}

	// Every loop, archived included: an archived loop's token still opens
	// its bot, and its old transcripts are still served.
	loops, err := s.Store.Loops().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range loops {
		if l.TGBotToken != "" {
			out = append(out, Secret{Name: "tg_bot_token", Value: l.TGBotToken})
		}
		if l.HubMCPToken != "" {
			out = append(out, Secret{Name: "hub_mcp_token", Value: l.HubMCPToken})
		}
		secrets, err := s.Store.LoopSecrets().List(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		for _, sec := range secrets {
			out = append(out, Secret{Name: sec.Name, Value: sec.Value})
		}
	}
	return out, nil
}
