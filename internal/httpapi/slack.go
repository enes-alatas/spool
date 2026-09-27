package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// The Slack half of the control-room contract posted on #230: a loop is
// given a Slack app by pasting its two tokens, and the codes below are what
// the attach form keys its messages on.
const (
	codeSurfaceInUse          = "surface_in_use"
	codeSlackBotTokenRejected = "slack_bot_token_rejected"
	codeSlackAppTokenRejected = "slack_app_token_rejected"
	codeSlackWorkspace        = "slack_workspace_mismatch"
)

// requestError is a refusal a helper decided on and its handler sends.
type requestError struct {
	status int
	code   string
	msg    string
}

func (server *Server) refuse(w http.ResponseWriter, refusal *requestError) {
	if refusal.code == "" {
		server.jsonErr(w, refusal.status, "%s", refusal.msg)
		return
	}
	server.jsonErrCode(w, refusal.status, refusal.code, "%s", refusal.msg)
}

// oneSurface refuses a request that would leave a loop on two surfaces
// (ADR-0029 item 7). current is the surface the loop is on now, "" when the
// request itself names both.
func oneSurface(current string) *requestError {
	if current == "" {
		return &requestError{status: http.StatusBadRequest,
			msg: "a loop has one surface: send a Telegram bot token or a Slack app's tokens, not both"}
	}
	return &requestError{status: http.StatusConflict, code: codeSurfaceInUse,
		msg: "a loop has one surface, and this one is on " + current + ": detach it first"}
}

// slackIdentity turns a request's pair of Slack tokens into the identity to
// store for loopID. Two empty tokens detach the app, and come back as the
// zero identity. The tokens travel together: one Slack app is one pair.
func (server *Server) slackIdentity(ctx context.Context, loopID, appToken, botToken string) (store.SlackIdentity, *requestError) {
	appToken, botToken = strings.TrimSpace(appToken), strings.TrimSpace(botToken)
	if appToken == "" && botToken == "" {
		return store.SlackIdentity{}, nil
	}
	if appToken == "" || botToken == "" {
		return store.SlackIdentity{}, &requestError{status: http.StatusBadRequest,
			msg: "slack_app_token and slack_bot_token go together: one Slack app is one pair"}
	}
	identity := store.SlackIdentity{AppToken: appToken, BotToken: botToken}
	slack := server.Surfaces[store.SurfaceSlack]
	if slack == nil {
		// A hub running without the Slack surface stores the pair as given,
		// as it does a Telegram token with no Telegram surface.
		return identity, nil
	}
	// Live calls to Slack, which is why the handlers write through
	// LoopEdit rather than a copy read before them (#164).
	named, err := slack.ValidateCredential(ctx, surface.Credential{Token: botToken, AppToken: appToken})
	switch surface.RejectedPart(err) {
	case surface.PartToken:
		return identity, &requestError{status: http.StatusBadRequest, code: codeSlackBotTokenRejected,
			msg: "slack bot token rejected: " + err.Error()}
	case surface.PartAppToken:
		return identity, &requestError{status: http.StatusBadRequest, code: codeSlackAppTokenRejected,
			msg: "slack app-level token rejected: " + err.Error()}
	}
	if err != nil {
		return identity, &requestError{status: http.StatusBadGateway, msg: "slack did not answer: " + err.Error()}
	}
	// The fleet channel is one channel, and a channel lives in one
	// workspace, so a hub's Slack loops share one (#230).
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return identity, &requestError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	for _, other := range loops {
		if other.ID != loopID && other.SlackTeamID != "" && other.SlackTeamID != named.TeamID {
			return identity, &requestError{status: http.StatusConflict, code: codeSlackWorkspace,
				msg: "this hub's Slack loops are in workspace " + describeTeam(other.SlackTeamName, other.SlackTeamID) +
					", and this app is in " + describeTeam(named.TeamName, named.TeamID)}
		}
	}
	identity.BotUserID, identity.BotName = named.UserID, named.Name
	identity.TeamID, identity.TeamName = named.TeamID, named.TeamName
	return identity, nil
}

func describeTeam(name, id string) string {
	if name == "" {
		return id
	}
	return name + " (" + id + ")"
}

// handleSlackStatus reports a loop's Slack app and what the surface is doing
// with it, in the shape posted on #230.
func (server *Server) handleSlackStatus(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	status := map[string]any{
		"configured":  loopRecord.SlackBotToken != "",
		"bot_user_id": loopRecord.SlackBotUserID,
		"bot_name":    loopRecord.SlackBotName,
		"team_id":     loopRecord.SlackTeamID,
		"team_name":   loopRecord.SlackTeamName,
		"channel_id":  loopRecord.SlackChannelID,
		// Named from conversations.info when the connection binds a
		// channel, which the Socket Mode slice of #230 brings.
		"channel_name": "",
	}
	if slack := server.Surfaces[store.SurfaceSlack]; slack != nil {
		status["bridge"] = slack.Status(loopRecord.ID)
	}
	writeJSON(w, http.StatusOK, status)
}
