package redact

import (
	"context"
	"errors"

	"github.com/enes-alatas/spool/internal/store"
)

// StoreSource reads every secret Spool holds out of the store: the
// operator's Claude token, and per loop its surface credentials (a Telegram
// bot token, or a Slack app's two tokens) and its hub MCP token; and every
// connection's secret, the loops' tool secrets among them.
//
// It reads through the *undecorated* store. Nothing here is persisted, so
// there is nothing to redact, and a redactor asking a redacted store for the
// values it redacts by is a loop worth not building.
type StoreSource struct{ Store store.Store }

func (source StoreSource) Secrets(ctx context.Context) ([]Secret, error) {
	var out []Secret

	token, err := source.Store.Settings().Get(ctx, store.SettingClaudeOAuthToken)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if token != "" {
		out = append(out, Secret{Name: store.SettingClaudeOAuthToken, Value: token})
	}

	// Every loop, archived included: an archived loop's token still opens
	// its bot, and its old transcripts are still served.
	loops, err := source.Store.Loops().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, loopRecord := range loops {
		if loopRecord.TGBotToken != "" {
			out = append(out, Secret{Name: "tg_bot_token", Value: loopRecord.TGBotToken})
		}
		// A Slack app is two credentials, and either one opens it: the app
		// token opens its Socket Mode connection, the bot token posts as it.
		if loopRecord.SlackAppToken != "" {
			out = append(out, Secret{Name: "slack_app_token", Value: loopRecord.SlackAppToken})
		}
		if loopRecord.SlackBotToken != "" {
			out = append(out, Secret{Name: "slack_bot_token", Value: loopRecord.SlackBotToken})
		}
		if loopRecord.HubMCPToken != "" {
			out = append(out, Secret{Name: "hub_mcp_token", Value: loopRecord.HubMCPToken})
		}
	}

	// Every connection, attached or not: a value the hub holds is one a
	// later attachment can hand a loop (ADR-0043). An env-var is named by its
	// variable, as the per-loop secret it replaced was: what was removed was
	// a GH_TOKEN, whichever connection held it.
	connections, err := source.Store.Connections().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if connection.Secret == "" {
			continue
		}
		name := "connection:" + connection.Name
		if connection.Kind == store.ConnectionEnvVar {
			name = connection.Config.Env
		}
		out = append(out, Secret{Name: name, Value: connection.Secret})
	}
	return out, nil
}
