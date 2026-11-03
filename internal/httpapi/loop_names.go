package httpapi

import (
	"context"
	"strings"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// namesInUse is every name a group mention can mean on the hub, lower-cased:
// a loop's name, a loop's bot username, the username of a person the hub
// allows on Telegram or Slack, and a control room user's name (#1273). A
// loop named like any of them would take mentions meant for another.
type namesInUse map[string]nameInUse

// nameInUse is whose a name is, and the words that say so.
type nameInUse struct {
	kind nameKind
	// loopID is the loop a name or a bot username belongs to.
	loopID string
	whose  string
}

type nameKind int

const (
	reservedName nameKind = iota
	loopName
	botUsername
	personName
)

func (server *Server) namesInUse(ctx context.Context) (namesInUse, error) {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return nil, err
	}
	telegramSenders, err := server.Store.TGSenders().List(ctx)
	if err != nil {
		return nil, err
	}
	slackSenders, err := server.Store.SlackSenders().List(ctx)
	if err != nil {
		return nil, err
	}
	users, err := server.Store.Users().List(ctx)
	if err != nil {
		return nil, err
	}
	names := namesInUse{route.BroadcastToken: {kind: reservedName, whose: "@" + route.BroadcastToken}}
	// every loop's name goes in before any bot username, as a mention of
	// a name routes to the loop of that name
	for _, loopRecord := range loops {
		names.add(loopRecord.Name, nameInUse{loopName, loopRecord.ID, "the name of the loop " + loopRecord.Name})
	}
	for _, loopRecord := range loops {
		if loopRecord.TGBotUsername != "" {
			names.add(loopRecord.TGBotUsername, nameInUse{botUsername, loopRecord.ID,
				"the username of the loop " + loopRecord.Name + "'s bot"})
		}
	}
	for _, sender := range telegramSenders {
		if sender.Status == store.SenderAllowed && sender.Username != "" {
			names.add(sender.Username, nameInUse{kind: personName, whose: "the Telegram username of a person the hub allows"})
		}
	}
	for _, sender := range slackSenders {
		if sender.Status == store.SenderAllowed && sender.Username != "" {
			names.add(sender.Username, nameInUse{kind: personName, whose: "the Slack username of a person the hub allows"})
		}
	}
	for _, user := range users {
		names.add(user.Name, nameInUse{kind: personName, whose: "the name of a control room user"})
	}
	return names, nil
}

func (names namesInUse) add(name string, use nameInUse) {
	key := strings.ToLower(strings.TrimPrefix(name, "@"))
	if _, taken := names[key]; !taken {
		names[key] = use
	}
}

// taken says whether a name is in use, ignoring case.
func (names namesInUse) taken(name string) bool {
	_, taken := names[strings.ToLower(name)]
	return taken
}

// nameRefusal refuses a new loop's name that is in use as a bot username
// or a person's. @all's and another loop's name are refused where they
// were before: the one by its own words, the other by the store.
func (names namesInUse) nameRefusal(name string) *requestError {
	use, taken := names[strings.ToLower(name)]
	if !taken || use.kind == reservedName || use.kind == loopName {
		return nil
	}
	return refusalf(409, codeNameInUse, "%q is %s; a loop of that name would take the mentions meant for it", name, use.whose)
}

// botRefusal refuses a bot username that is the name of a loop other than
// the one with loopID: a mention of it would mean two loops.
func (names namesInUse) botRefusal(username, loopID string) *requestError {
	use, taken := names[strings.ToLower(username)]
	if !taken || use.kind != loopName || use.loopID == loopID {
		return nil
	}
	return refusalf(409, codeNameInUse, "the bot @%s has %s; a mention of it would mean both loops", username, use.whose)
}

// codeNameInUse marks a refused name or bot username: a mention of it
// would mean someone else as well.
const codeNameInUse = "name_in_use"
