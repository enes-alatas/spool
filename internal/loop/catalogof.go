package loop

import (
	"context"
	"log/slog"
	"slices"

	"github.com/enes-alatas/spool/internal/store"
)

// CatalogOf resolves who a loop can address, fresh for each prompt build:
// its conversations and the channels among them, its peers in those
// channels, its team, the people allowed to talk to the fleet, and its own
// owner with whether a private chat to them exists yet (#45, #288).
func CatalogOf(db store.Store, self *store.Loop) Catalog {
	ctx := context.Background()
	// The caller's copy is the actor's, taken when it last loaded the loop;
	// ownership changes through the API, so read it back before describing
	// who this loop can reach.
	if fresh, err := db.Loops().Get(ctx, self.ID); err == nil {
		self = fresh
	}
	loops, loopsErr := db.Loops().List(ctx)
	// a hub whose channels cannot be read teaches no channel, and one whose
	// rooms cannot be read teaches its channels as hub-only; either is
	// logged, since the loop is told it reaches less than it does until
	// its next prompt build reads them
	channels, err := db.Channels().List(ctx)
	if err != nil {
		slog.Warn("catalog: channels unreadable, teaching none", "loop", self.Name, "err", err)
	}
	rooms, err := db.Rooms().List(ctx, self.ID)
	if err != nil {
		slog.Warn("catalog: rooms unreadable, teaching channels hub-only", "loop", self.Name, "err", err)
	}
	cat := Catalog{Conversations: ConversationsOf(self, channels, loops, rooms), OwnerDMReady: self.OwnerDMReady()}
	cat.BotUsername, _ = botOf(self)
	// a team that cannot be read goes unnamed, as its channels still list
	if self.Team != "" {
		if team, err := db.Teams().Get(ctx, self.Team); err == nil {
			cat.Team = &Team{Name: team.Name, Description: team.Description}
		}
	}
	if loopsErr != nil {
		return cat
	}
	// a loop that shares no channel with this one is reached by none of its
	// mentions, so naming it would teach a mention that goes nowhere
	shared := map[string]bool{}
	for _, channel := range channels {
		if slices.Contains(channel.LoopIDs, self.ID) {
			for _, id := range channel.LoopIDs {
				shared[id] = true
			}
		}
	}
	// a paused loop is listed, marked: a pause holds it back and takes it
	// out of no channel, so a mention still reaches it (#1648)
	for _, loopRecord := range loops {
		if loopRecord.ID != self.ID && loopRecord.Status != store.StatusArchived && shared[loopRecord.ID] {
			bot, onSurface := botOf(loopRecord)
			cat.Peers = append(cat.Peers, Peer{
				Name: loopRecord.Name, Mission: loopRecord.Mission, BotUsername: bot, Surface: onSurface,
				Paused: loopRecord.Status == store.StatusPaused,
			})
		}
	}
	// oldest first, so the catalog reads in the order people joined and
	// does not reshuffle between wakes
	// the allowed people by their stable key, to name the ones who have
	// messaged this loop privately
	allowed := map[string]Person{}
	if senders, err := db.TGSenders().List(ctx); err == nil {
		for i := len(senders) - 1; i >= 0; i-- {
			sender := senders[i]
			if sender.Status != store.SenderAllowed {
				continue
			}
			person := Person{Username: sender.Username, Display: sender.Display, TGUserID: sender.TGUserID}
			cat.People = append(cat.People, person)
			allowed[store.TGPerson(sender.TGUserID)] = person
			// every loop carries the hub's default Telegram owner, so a
			// Slack loop's owner is only ever read from its Slack senders
			if sender.TGUserID == self.OwnerTGUserID && self.Surface() != store.SurfaceSlack {
				owner := person
				cat.Owner = &owner
			}
		}
	}
	if senders, err := db.SlackSenders().List(ctx); err == nil {
		for i := len(senders) - 1; i >= 0; i-- {
			sender := senders[i]
			if sender.Status != store.SenderAllowed {
				continue
			}
			person := Person{Username: sender.Username, Display: sender.Display, SlackUserID: sender.SlackUserID}
			cat.People = append(cat.People, person)
			allowed[store.SlackPerson(sender.SlackUserID)] = person
			if sender.SlackUserID == self.OwnerSlackUserID && self.Surface() == store.SurfaceSlack {
				owner := person
				cat.Owner = &owner
			}
		}
	}
	// the people other than the owner who have messaged this loop privately
	// on the surface it is on now: the ones it can write to first (ADR-0049)
	if chats, err := db.PrivateChats().List(ctx, self.ID); err == nil {
		owner := store.SlackPerson(self.OwnerSlackUserID)
		if self.Surface() == store.SurfaceTelegram {
			owner = store.TGPerson(self.OwnerTGUserID)
		}
		for _, chat := range chats {
			person, ok := allowed[chat.Person]
			if ok && store.PersonSurface(chat.Person) == self.Surface() && chat.Person != owner {
				cat.Private = append(cat.Private, person)
			}
		}
	}
	return cat
}

// botOf names the bot a loop posts as and the surface it posts on, or two
// empty strings for a loop with none.
func botOf(loopRecord *store.Loop) (bot, onSurface string) {
	switch loopRecord.Surface() {
	case store.SurfaceTelegram:
		return loopRecord.TGBotUsername, store.SurfaceTelegram
	case store.SurfaceSlack:
		return loopRecord.SlackBotName, store.SurfaceSlack
	}
	return "", ""
}

// FleetRulesOf reads the fleet rules fresh for each prompt build, so an edit in
// the control room reaches every loop on its next wake (ADR-0024).
func FleetRulesOf(db store.Store) []*store.FleetRule {
	rules, err := db.FleetRules().List(context.Background())
	if err != nil {
		return nil
	}
	return rules
}
