# ADR-0037: Attachments are kept by the hub and read from the workstation

Date: 2026-09-30 · Status: accepted. The shape follows the operator's decisions of 2026-09-30 on #123, including item 4's change to the SandboxRuntime seam. · Amends: ADR-0004 (the SandboxRuntime seam gains `PutFile`) · Amended: 2026-09-30 (the outbound half, ADR-0026)

## Context

A human could not send a loop a screenshot, and a loop could not send one
back (#123). The operator settled the contract on 2026-09-30:
- An attachment is stored once by the hub.
- A loop is shown it as a file path it can read in its workstation, and an
  image also gets a one-line description.
- The limit is 20 MB per attachment, and files are kept for 30 days.
- A loop sends one back by naming a path in `send_message`.

This ADR records the inbound half. The outbound half amends ADR-0026 when
it lands.

The constraint that shapes it is the docker workstation. Its home is a
named volume, not a bind mount, so the hub has no host path into it, and a
loop there cannot read a host file the hub holds.

## Decision

1. **The hub keeps each file once**, under `<data-dir>/files/`, with a
   random prefix and a sanitized name (`internal/attach`). An `attachments`
   row references it from its message, one row per file. A Slack message
   can carry several files.
   - Two messages carrying the same bytes each get their own copy, so
     expiring one never removes the other's.
2. **Limits.** A file over 20 MB is not kept. Its row records `too_large`,
   and the loop is told it was sent but not kept. A file the surface would
   not hand over is recorded as `fetch_failed` in the same way. A file is
   downloaded only after its message is stored, so of several surfaces that
   hear one message, only the one that ingests it fetches the file.
3. **Retention.** An hourly job removes files older than 30 days and sets
   `removed_at`. The row stays, so a message still says what it carried.
   Unlike events, this is not a flag: 30 days is the operator's decision.
4. **The SandboxRuntime seam gains `PutFile(ctx, loopID, hostPath, path)`.**
   The router names each attachment by a path in the recipient's
   workstation. A bare loop runs on the host, so it is shown the hub's own
   copy and needs no copy made. Any other runtime is shown
   `/home/loop/.spool/files/<name>`, and before the turn that reads it the
   actor copies the file there with `PutFile`.
   - Docker streams the file in over `docker exec` stdin, as the
     workstation's own user, so the loop owns what it is given. The same
     exec clears files there older than 30 days, because they are copies of
     the hub's and would otherwise outlive them.
   - A copy that fails does not stop the turn. The turn opens with a system
     note naming the paths that hold nothing.
5. **The envelope** shows each attachment on its own line, under the header
   and above the words (the words are usually about the attachment):

   ```
   [image: shot.png · 1280×720 · 240 KB · /home/loop/.spool/files/…-shot.png]
   [file not kept: dump.bin · 34.1 MB · over the 20 MB limit]
   ```

   The "one-line description" of an image is this line: the kind, the
   dimensions read from the file's header, and the size. It is not a model's
   caption. The hub has no vision model, and adding one would mean a new
   dependency and a token spend.

## Consequences

- A photo sent with only a caption now reaches the loop. It used to be
  dropped at the empty-text check.
- A docker workstation holds copies of files for up to 30 days, in its own
  volume.
- The seam's substitutability holds: a new runtime has to implement
  `PutFile`, and a bare-like runtime can do it trivially.

**Amendment (2026-09-30, #123):** the outbound half landed as ADR-0026's
`attach` field. A file a loop sends is kept here like an inbound one, on
the same 20 MB limit and 30-day retention, and a runtime now implements
`GetFile` as well, to read it out of the workstation.
