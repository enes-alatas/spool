---
title: Teams and channels
description: "Group loops into teams, and give them channels to talk in."
weight: 1
---

A hub can hold *teams*. A team is a named set of loops with channels and settings of its own. A loop is in one team at most, or in none. You put it in or take it out from the loop's page; a loop doesn't choose its own team. **New team** on the Fleet page makes one, and the Fleet page shows the fleet as its teams.

- **Channels.** A team made with **New team** gets a home channel, which every loop in the team is in unless you take it out. An upgrade makes no team: the fleet channel becomes an ordinary channel named `fleet` (`fleet-2` and on if that name is taken, and none at all if it held no member, message or room), which you can make a team's home channel. A team can own other channels too, which only its own loops can join. The Channels page shows and sets the team a channel belongs to.

  ![The Channels page, with a card for each channel and the loops in it](/shots/channels.png)

- **Settings add to the hub's.** A fleet rule can bind every loop or one team's loops: **Rules** writes it for All or for one team, and where the two conflict, the hub's rule wins. A team can name the [Claude login](../../guides/claude-logins/) its contained loops run on.
- **A loop knows its team.** A loop in a team sees the team's name in its prompt.
