# ADR-0046: The hub seals the secrets it stores under a hub key

Date: 2026-10-07 · Status: accepted (operator decisions of 2026-10-07 13:52 and 14:52 UTC, recorded on #623) · Amends: ADR-0017 (decision 5: token custody), ADR-0043 (item 2: the database's plain text) · Relates to: ADR-0030, ADR-0045

## Context

Every credential the hub keeps sat in `spool.db` in plain text: each
connection's secret and the secrets retired from them (ADR-0043), each
loop's Telegram and Slack bot tokens, each loop's hub MCP token (ADR-0026),
and the operator's Claude setup-token (ADR-0017). Since #149 the data
directory is `0700` and the files in it `0600`, which keeps other accounts
on the machine out. It does nothing for a copy of the file: a backup, a
synced home directory, a disk image, a database attached to a bug report.
Whoever holds the copy holds every tool credential the fleet uses and the
operator's Claude login. The operator asked on 2026-10-06 whether secrets
were encrypted in the database, and decided they should be (#623).

## Decision

1. **A hub key seals every stored credential.** The key is 32 random bytes
   in `<data-dir>/hub.key`, written in hex, mode `0600`, minted at the first
   start. Every value in these columns is sealed with AES-256-GCM under a
   fresh nonce and opened as the store reads it:
   - `connections.secret` and `retired_secrets.value`;
   - `loops.tg_bot_token`, `slack_app_token`, `slack_bot_token` and
     `hub_mcp_token`;
   - the setup-token's settings value.
   A sealed value is stored as `sealed:v1:` and the base64 of its nonce and
   ciphertext, so the format can change later and still read this one. An
   empty value stays empty, since it holds nothing. The cipher is the Go
   standard library's, so no dependency is added.

2. **Comparisons move out of SQL.** Two seals of one value never match.
   - Rotating a connection to the value it already holds is still a no-op:
     the store opens the current value and compares in Go.
   - A loop is found by its hub MCP token through the token's SHA-256, in
     `hub_mcp_token_hash`, which carries the unique index the token had. A
     plain hash is enough because the token is 24 random bytes, too many to
     guess from a hash.

3. **The key is checked before anything is read with it.** The database
   keeps a known value sealed under its key. A start whose key doesn't open
   it is refused, as is a start with sealed values and no key at all. A
   lost key is never quietly replaced by a new one, which would leave every
   secret unreadable without saying so.

4. **An existing database is sealed at its first start with a key.** Every
   value in the columns above still in plain text is sealed, the token
   hashes are filled, and the check value is written, in one transaction.
   The database is then rewritten whole (`VACUUM`), because SQLite leaves a
   replaced value in the file's free space, where a copy would still carry
   it. The sweep runs at every start, so a value written around the store
   is sealed at the next one.

5. **What this protects, and what it doesn't.**
   - It protects **a copy of `spool.db`** taken without `hub.key`. The
     database alone holds no credential.
   - It does not protect **a copy of the whole data directory**, since the
     key is beside the database. The operator backs `hub.key` up apart
     from the database: a backup holding both is as sensitive as the
     credentials in it, and without the key they are gone.
   - It does not protect against **root or the hub's own user**, who can
     read the key. A bare loop runs as the operator's user, so it can read
     both files (ADR-0017, ADR-0045).
   - It does not change **secrets in flight to a loop**, which are plain by
     necessity: an env var, an mcp-config file (ADR-0045).

6. **Losing the key loses the secrets.** Nothing else can open them. Until
   the operator acts, the hub doesn't start, and says how to recover:
   - **Restore `hub.key`**, from wherever the operator keeps it.
   - **Or start once with `--forget-secrets`** (operator, 2026-10-07). The
     hub gives up what it can't open and seals the database afresh, under
     the key beside it, or a new one if there is none or the file holds no
     key:
     - every connection holding a secret is revoked, and the retired
       values go;
     - each loop's Telegram and Slack bots are unbound;
     - each loop's hub MCP token is minted again;
     - the setup-token is cleared.
     The operator then enters the secrets still needed. With the right key
     the flag changes nothing, so it is harmless left in a service unit.

   A `hub.key` that holds no key at all counts as a wrong one, and the flag
   replaces it too. A new key is written to a temporary file and synced
   before it takes the name, so a first start cut short leaves no partial
   `hub.key`, and a damaged one beside a database with nothing sealed yet is
   simply replaced.

7. **Later slices** (operator, 2026-10-07):
   - Moving the key into the OS keyring, which takes a dependency.
   - Per-user keys arrive with users (L5, #582), and hosted key management
     with the hosted service (L7).

## Consequences

- A database attached to an issue, synced or backed up without its key
  carries no usable credential.
- The data directory now holds a second file that must not leave the
  machine with the database. `internal/datadir` keeps it `0600` with the
  others.
- A secret is opened on every read of its row, which is a few microseconds
  of AES-GCM against a SQLite read; the cost doesn't show.
- A hub that starts against a database whose key is gone stops, with a
  message naming the file and `--forget-secrets`. Before this, the same hub
  would have started.
- Tools that read `spool.db` directly see sealed values. Nothing in the tree
  does outside the store and its tests.

## Alternatives considered

- **SQLCipher, or another whole-file encryption.** It needs cgo or a
  driver we don't have, and the pure-Go driver (ADR-0003) has no
  equivalent. Sealing the columns that hold secrets does the job without
  either.
- **A key derived from a passphrase the operator types at each start.** A
  hub that restarts on its own, under a service manager, would wait at a
  prompt. It may come back as an option with the keyring slice.
- **The key inside the database.** A copy of the file would carry the key
  with it, which protects nothing.
