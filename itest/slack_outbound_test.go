//go:build integration

package itest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A loop's group send is its own app's post in the channel the app is bound
// to, with a mention of another loop written as Slack's mention of its app
// (#230). A human's reply in the post's thread is a reply to the loop's
// message and reaches it; the loop's answer to that goes in the same
// thread. A post Slack will not take is a failure the operator can retry,
// and a loop whose app is bound to no channel keeps its words on the hub.
func TestSlackGroupSendIsTheAppsPost(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	// terra's app binds to the channel an allowed sender speaks in; milo's
	// hears nothing, so it is bound to none
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "@milo terra's words"}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	post := slack.waitPost(t, "terra's words")
	if post.Token != slackBotToken || post.Channel != slackChannel || post.Text != "<@U0MILO> terra's words" || post.ThreadTS != "" {
		t.Fatalf("terra's words were posted as %+v", post)
	}
	sent := srv.waitGroupMessage("@milo terra's words", mirrorIs("mirrored"))

	slack.pushReply(t, slackAppToken, "channel", slackChannel, slackOperator, "why?", "1727600000.000020", post.TS)
	reply := srv.waitGroupMessage("why?", func(m activityMessage) bool { return m.ReplyToID != 0 })
	if reply.ReplyToID != sent.ID {
		t.Fatalf("a reply in the post's thread answers message %d, want terra's %d", reply.ReplyToID, sent.ID)
	}
	srv.waitInput("terra", "why?", fmt.Sprintf("in reply to ref:%d", sent.ID))

	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "because",
		"reply_to": fmt.Sprintf("ref:%d", reply.ID)}); res.IsError {
		t.Fatalf("terra's reply refused: %s", resultText(res))
	}
	if answer := slack.waitPost(t, "because"); answer.ThreadTS != post.TS {
		t.Fatalf("terra's reply went to thread %q, want its post's %q", answer.ThreadTS, post.TS)
	}

	slack.setFailPosts(-1)
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "@milo words that fail"}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	failed := srv.waitGroupMessage("@milo words that fail", func(m activityMessage) bool { return m.SendFailedAt != 0 })
	slack.setFailPosts(0)
	srv.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", failed.ID), nil, nil)
	slack.waitPost(t, "words that fail")
	srv.waitGroupMessage("@milo words that fail", mirrorIs("mirrored"))

	milo := mcpSession(t, srv, hubMCPToken(t, srv, "milo"))
	if res := callSend(t, milo, map[string]any{"destination": "group", "text": "@terra milo's app is in no channel"}); res.IsError {
		t.Fatalf("milo's group send refused: %s", resultText(res))
	}
	srv.waitGroupMessage("@terra milo's app is in no channel", mirrorIs("not_mirrored"))
	for _, taken := range slack.postsTaken() {
		if taken.Token == slackMiloBotToken {
			t.Fatalf("milo's app posted with no channel bound: %+v", taken)
		}
	}
}

// owner_dm on Slack is the owner's DM with the loop's app, which the app
// opens itself when the owner has never written: unlike a Telegram bot, a
// Slack app can write first (#230). Only the Slack surface carries it, so
// the row is mirrored and carries no failure from any other surface.
func TestSlackOwnerDMOpensTheDM(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": slackOperator}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}
	if !srv.loop("terra").OwnerDMReady {
		t.Fatal("a Slack loop with an owner does not read owner_dm_ready")
	}

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	if res := callSend(t, terra, map[string]any{"destination": "owner_dm", "text": "for your eyes only"}); res.IsError {
		t.Fatalf("terra's owner_dm refused: %s", resultText(res))
	}
	post := slack.waitPost(t, "for your eyes only")
	if post.Token != slackBotToken || post.Channel != "D"+slackOperator {
		t.Fatalf("owner_dm was posted as %+v, want terra's app in the DM it opened with the owner", post)
	}
	var sent activityMessage
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got := srv.activityWith("for your eyes only"); len(got) == 1 && got[0].Mirror == "mirrored" {
			sent = got[0]
			break
		}
	}
	if sent.Mirror != "mirrored" || sent.SendFailedAt != 0 || sent.Conversation != "owner_dm" {
		t.Fatalf("the owner_dm row reads %s", dump(srv.activityWith("for your eyes only")))
	}

	// The DM the app opened is the owner's: the owner writing in it reaches
	// terra, and a later send goes to it without opening another.
	slack.pushMessage(t, slackAppToken, "im", "D"+slackOperator, slackOperator, "got it", "1727600000.000500")
	srv.waitInput("terra", "via slack dm · owner_dm", "got it")
}

// A Slack loop's system prompt names the app it posts as and its peers'
// apps, teaches Slack's envelope headers, and knows owner_dm is open to it
// as soon as it has an owner.
func TestSlackCatalog(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	slack.addApp(slackMiloBotToken, slackMiloAppToken, miloBot)
	srv := startSlackServer(t, slack)
	ws := workspaceWithScript(t, "!sysprompt\n")
	terraReq := slackPair(slackAppToken, slackBotToken)
	terraReq["workspace_path"], terraReq["workspace_mode"] = ws, "dir"
	srv.createLoop("terra", terraReq)
	srv.createLoop("milo", slackPair(slackMiloAppToken, slackMiloBotToken))
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.Connected })
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "hello", "1727600000.000001")
	srv.allowSlackSender(slackOperator)
	srv.mustJSON("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": slackOperator}, nil)

	first := waitPrompt(t, srv, "terra", 0)
	prompt := waitPromptAfterRotation(t, srv, "terra", first.SessionID)
	for _, want := range []string{
		"You are @terra, posting in slack as @terra",
		"(posts as @milo in slack)",
		"Your owner is @u0oper",
		"owner_dm reaches them privately",
		"your owner's private Slack chat",
		`"[message from @enes via slack · group · ref:42 · ...]"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("a Slack loop's prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// A Slack loop's owner is its Slack owner only. On a hub with an allowed
// Telegram sender every loop carries that sender as its Telegram owner,
// and attaching a Slack app clears nothing, so a Slack loop with no Slack
// owner must still read as having none: owner_dm refused as not configured,
// and a prompt that says so rather than teaching Telegram's "message the
// bot once".
func TestSlackLoopIgnoresTheTelegramOwner(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	tg := startFakeTelegram(t, "tgbot")
	srv := startServerArgs(t, t.TempDir(), "--runtime", "bare",
		"--slack-api-base", slack.srv.URL, "--telegram-api-base", tg.srv.URL)
	ws := workspaceWithScript(t, "!sysprompt\n")
	terraReq := slackPair(slackAppToken, slackBotToken)
	terraReq["workspace_path"], terraReq["workspace_mode"] = ws, "dir"
	srv.createLoop("terra", terraReq)
	srv.createLoop("on-telegram", map[string]any{"tg_bot_token": "tgbot"})

	operator := user{ID: 5454, First: "Operator", Username: "operator"}
	tg.post(groupChatID, "supergroup", "hello", operator)
	srv.allowSender(operator.ID)
	if got := srv.loop("terra").OwnerTGUserID; got != operator.ID {
		t.Fatalf("the Slack loop's Telegram owner is %d, want the adopted %d: the row proves nothing", got, operator.ID)
	}

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	res := callSend(t, terra, map[string]any{"destination": "owner_dm", "text": "anyone there?"})
	if !res.IsError || !strings.Contains(resultText(res), "owner_not_configured") {
		t.Fatalf("owner_dm from a Slack loop with no Slack owner: %s", resultText(res))
	}

	first := waitPrompt(t, srv, "terra", 0)
	prompt := waitPromptAfterRotation(t, srv, "terra", first.SessionID)
	if !strings.Contains(prompt, "You have no owner configured") || strings.Contains(prompt, "Your owner is") {
		t.Fatalf("a Slack loop with no Slack owner reads its owner as:\n%s", prompt)
	}
}

// A long message goes out as several posts, and one after the first that
// never landed used to be logged and forgotten, the row reading mirrored
// (#302). Now the message is a failure that says which part. And a send
// the app was still retrying when it was detached fails then, rather than
// sit pending until the hub's next start.
func TestSlackLostSendsAreFailures(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })
	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))

	long := "@milo the start " + strings.Repeat("filler ", 600) + "the lost tail"
	slack.failPostsContaining("the lost tail")
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": long}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	slack.waitPost(t, "the start")
	failed := srv.waitGroupMessage(long, func(m activityMessage) bool { return m.SendFailedAt != 0 })
	if !strings.Contains(failed.SendError, "part 2 of 2") || failed.Mirror == "mirrored" {
		t.Fatalf("a message missing its tail reads %s", dump(failed))
	}
	slack.failPostsContaining("")

	slack.setFailPosts(-1) // retried with a backoff of seconds: the window
	before := slack.postAttempts()
	const held = "@milo held when the app went"
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": held}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	for deadline := time.Now().Add(10 * time.Second); slack.postAttempts() == before; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the app never attempted the send")
		}
	}
	srv.mustJSON("PATCH", "/api/loops/terra", slackPair("", ""), nil)
	dropped := srv.waitGroupMessage(held, func(m activityMessage) bool { return m.SendFailedAt != 0 })
	if !strings.Contains(dropped.SendError, "replaced or detached before this was sent") {
		t.Fatalf("a send its app dropped records %q", dropped.SendError)
	}
}
