---
title: Connections
description: "A secret or a tool for your loops, such as a token, an MCP server or a GitHub App, defined once."
weight: 3
---

A connection is a secret or a tool you give your loops: a `GH_TOKEN`, an MCP server, a GitHub App. You define it once on the **Connections** page and attach it to the loops, or the teams, that need it. Only an admin adds, attaches or changes one. A member sees each connection's name and who holds it, but not what it points at.

## The kinds

| Kind | What it holds | What a loop gets |
|---|---|---|
| **env variable** | A variable name, such as `GH_TOKEN`, and its value | The variable in its environment |
| **MCP server · http** | A URL, and an optional secret | One more MCP server, reached through the hub |
| **MCP server · stdio** | A command and its arguments, and an optional secret | One more MCP server, started as a process |
| **GitHub App** | The app's ID, its installation ID and its private key | GitHub tokens for `git` and `gh`, fetched as needed |

Every value you enter as a secret is write-only. No page or API response shows it again; the page only says `secret set`. The hub stores it sealed under its hub key and redacts it from everything the hub records. A loop that holds an env variable can still read its value, so give a loop only what it may use.

## Adding one

On **Connections**, the plus at the right of the title opens **New connection**. Pick the kind, then:

- **Name.** `a-z`, `0-9` and `-`, up to 32 characters, not starting with `-`. It can't be changed later. An MCP server can't be named `spool`: that is the hub's own server.
- **env variable:** the variable name a loop's tools read, and its value. The value is required.
- **MCP server · http:** the server's URL. The URL is shown back in full, so put no credential in it: the hub refuses a URL with a `user:password@` in it. The secret, if the server needs one, is sent to the server as `Authorization: Bearer <secret>`. A secret on a plain `http://` URL is refused unless the host is this machine: `localhost`, a loopback address (`127.0.0.0/8`, `::1`), or an unspecified one (`0.0.0.0`, `::`), which also reaches this machine.
- **MCP server · stdio:** the command, its arguments one per line, and, if it needs a secret, the env variable the server reads it from. The secret is set in that server's process alone, never in the loop's shell.
- **GitHub App:** the App ID or Client ID (on the app's settings page on GitHub, under About), the installation ID (the number that ends the installation's settings URL), and the app's private key, the whole `.pem` file. The key must be an RSA key in PEM.

**Attach to** lets you give it to loops or teams as you create it. You can also do that later.

## Checking that it works

**Check** on a connection's row asks whether it works, so a wrong URL, key or installation shows when you save it, not at a loop's first session:

- An http MCP server is sent an MCP `initialize`. The secret goes with it only when a running loop holding the connection would send it the same way. Otherwise the answer is `unverified`.
- A GitHub App mints an installation token.
- An env variable and a stdio server aren't checked.

The hub keeps each connection's last result until its value changes. While any check has failed, a **Problems** pill above the list shows only those connections.

## Giving it to a loop

From the Connections page, pick a loop in a connection's **Attach to**; the × on a loop's pill detaches it. From a loop's page, the plus on its **Connections** panel offers three tabs:

- **attach existing**, a shared connection
- **create new**, a connection made and attached in one step
- **private variable**, an env variable for this loop alone (see [Private and shared](#private-and-shared))

When the loop sees the change:

- **An env variable** applies from the loop's next turn. The hub closes the loop's process at its next quiet moment, at once if it is idle or when its current turn ends, and the next turn starts with the new environment.
- **An MCP server** applies the next time the loop's `claude` process starts.
- **A GitHub App** applies from the next turn. The key never leaves the hub. The loop's `git` and `gh` ask the hub for a token each time they need one, and the hub hands back a token valid for at least 15 more minutes. A detach or a revoke refuses the next request.

A loop holds one GitHub identity. A second GitHub App, or a GitHub App beside an env variable on `GH_TOKEN`, is refused. So is a second env variable that sets a variable the loop already gets from another connection.

## Giving it to a team

**Attach to** lists your teams first, then the loops. Attaching a connection to a team shows it as one `team <name>` pill. Every loop in the team holds it, including loops that join later, and a loop that leaves the team loses it. A loop's page lists such a connection as `through team <name>`. It comes off with the × on the team's pill, never from one loop.

A loop that couldn't hold the connection, such as a second GitHub identity, refuses the attach, and the refusal names that loop. The same check refuses a loop joining a team whose connections it couldn't hold.

## Private and shared

An env variable added from a loop's page with **private variable** belongs to that loop. It isn't offered to any other loop. **Share with the fleet** on the loop's page, or **Share** on the connection's row, makes it a shared connection that any loop or team can take. Sharing can't be undone, because the value may already be in another loop's environment.

Deleting a private connection detaches it from its loop. Deleting a loop deletes the private connections that belonged to it.

## Changing a value

**Rotate value**, or **Replace key** for a GitHub App, sets a new value:

- **For an env variable or an MCP server,** every loop holding the connection ends its session at its next quiet moment. It writes a handoff note first, and the fresh session starts with the new value.
- **For a GitHub App,** no session ends. The token the loops were last handed is revoked at GitHub, and the next request gets a new one minted with the new key. The old key stays valid at GitHub until you delete it from the app's private keys.

The old value is never shown or handed to a loop again. The hub still redacts it, because a credential you replaced may still open something until you revoke it where it was issued.

## Revoking and deleting

**Revoke** is for a credential that has leaked. In one step it is taken from every loop and team that held it, its value is retired, and each of those loops ends its session. A revoked connection can't be attached, shared or given a new value again; create a new one instead.

**Delete** removes a connection that nothing holds. While any loop or team holds it, Delete stays disabled: detach it first.

## History

Every change to a connection is kept on an append-only record: created, attached, detached, shared, rotated, revoked, deleted. **History** on its row lists them, and a loop's **Connections** panel has the same list for that loop. A change reaches a loop at the moment described above, not at the time on its row.

## Cloud loops

A cloud loop gets its env variables when its session starts, not on its next turn. Stop and start the loop to apply a change at once. MCP servers never reach a cloud loop, and it can't hold a GitHub App. The values stay in the session on Anthropic's machines until you archive it on claude.ai. [Cloud loops](../cloud-loops/) has the details.
