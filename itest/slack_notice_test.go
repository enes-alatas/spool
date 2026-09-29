//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A person the app turns away is told why, once, in the DM they wrote in
// (#230): a sender Spool has not allowed gets their pairing code, and an
// allowed sender who DMs a loop that is not theirs learns whose it is. A
// pending sender in the channel gets no answer there, since the code is
// theirs to hand the operator.
func TestSlackTurnedAwaySenderIsToldWhy(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const stranger, strangerDM = "U0STRANGER", "D0STRANGER"
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, stranger, "anyone?", "1727600000.000100")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "let me in", "1727600000.000101")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "please", "1727600000.000102")

	const pairText = "Your pairing code is"
	told := slack.waitPost(t, pairText)
	if told.Token != slackBotToken || told.Channel != strangerDM || !strings.Contains(told.Text, slackPairCode(srv, stranger)) {
		t.Fatalf("the stranger was told %+v, want their code from terra's app in their DM", told)
	}

	const notOwnerText = "reads direct messages only from"
	operatorDM := "D" + slackOperator
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, "hi terra", "1727600000.000110")
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, "hello?", "1727600000.000111")
	if told := slack.waitPost(t, notOwnerText); told.Channel != operatorDM ||
		told.Text != "Spool: terra reads direct messages only from its owner for now. Reach it in its channel instead." {
		t.Fatalf("a non-owner was told %+v", told)
	}

	// each notice is said once; the second message of each pair, if it
	// were told again, is paced a second behind the first
	time.Sleep(2500 * time.Millisecond)
	for _, text := range []string{pairText, notOwnerText} {
		if n := countPosts(slack, text); n != 1 {
			t.Errorf("%q was posted %d times, want once: %s", text, n, dump(slack.postsTaken()))
		}
	}
}

// slackPairCode is the pairing code the hub gave a Slack sender.
func slackPairCode(srv *server, userID string) string {
	srv.t.Helper()
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
		PairCode    string `json:"pair_code"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	for _, sender := range senders {
		if sender.SlackUserID == userID && sender.PairCode != "" {
			return sender.PairCode
		}
	}
	srv.t.Fatalf("no pairing code for %s: %s", userID, dump(senders))
	return ""
}

// countPosts is how many posts any app has made containing text.
func countPosts(slack *fakeSlack, text string) int {
	n := 0
	for _, post := range slack.postsTaken() {
		if strings.Contains(post.Text, text) {
			n++
		}
	}
	return n
}

// A Slack owner hears of a refused Claude login in their DM with a loop's
// app, which opens it, once for the outage however many of their loops it
// stopped, and once more when it works again, from the same app (#419,
// #230).
func TestSlackOwnerIsToldOfARefusedLoginOnce(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	appOf := map[string]string{slackBotToken: "terra", slackMiloBotToken: "milo"}
	for _, name := range []string{"terra", "milo"} {
		srv.mustJSON("PUT", "/api/loops/"+name+"/owner", map[string]any{"slack_user_id": slackOperator}, nil)
		srv.message(name, "hi "+name)
		srv.waitTurn(name, 30*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "hi "+name)
		})
	}

	expired := filepath.Join(srv.fkState, "login-expired")
	if err := os.MkdirAll(srv.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.message(name, "are you there, "+name)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.waitState(name, "workstation_down", 30*time.Second)
	}
	const refusedText = "the Claude login was refused"
	told := slack.waitPost(t, refusedText)
	teller := appOf[told.Token]
	if told.Channel != "D"+slackOperator || !strings.Contains(told.Text, teller+" has stopped") {
		t.Fatalf("the owner was told %+v, want the notice naming the loop whose app posted it, in the owner's DM", told)
	}
	time.Sleep(2 * time.Second)
	if n := countPosts(slack, refusedText); n != 1 {
		t.Fatalf("the owner was told of one refused login %d times, want once", n)
	}
	if !srv.hasEvent(teller, "owner_notice", 5*time.Second) {
		t.Fatalf("%s told the owner without an owner_notice on its timeline", teller)
	}

	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.waitTurn(name, 60*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "are you there, "+name)
		})
	}
	const worksText = "the Claude login works again"
	if works := slack.waitPost(t, worksText); works.Token != told.Token || works.Channel != told.Channel {
		t.Fatalf("the all-clear was posted as %+v, want %s's app in the DM it told", works, teller)
	}
	time.Sleep(2 * time.Second)
	if n := countPosts(slack, worksText); n != 1 {
		t.Fatalf("the owner was told the login works again %d times, want once", n)
	}
}
