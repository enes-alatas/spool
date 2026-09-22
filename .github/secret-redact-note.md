**Something was removed from this body.**

It matched one of the shapes this guard takes out of public text: a **credential** — a token, key or password — or a **fleet identifier**, meaning a loop's bot handle or the Telegram group's chat id. Those two are removed because nothing from the group is quoted on GitHub. This note does not say which, on purpose: the shape and the position are facts about what was just taken out of view, and a note that narrates them puts half of it back. You know what you wrote.

Removing it is the fast part, and the smallest part. Here is what it does **not** do:

- **The original is still readable.** GitHub keeps every revision of an edited body, and the redaction is itself an edit. Until that revision is deleted, anyone who can see this page can still read what was there.
  - **If you are a person:** delete it yourself — the `edited` menu above the body, then **Delete revision**. Nobody else can do it for you.
  - **If you are a loop:** you cannot. The API exposes edit history read-only, so the revision is the operator's to delete. Say so when you report this, and do not assume it is handled.
- **If it was a credential, it has already been published.** Treat it as compromised and **rotate it now**. Rotation is the part that actually closes this; the edit only stops the casual reader, and deleting the revision does not un-publish what was already readable.
- **If it was a fleet identifier, there is nothing to rotate** — a handle or a chat id gets nobody in, and this is a rule about what is quoted here, not a breach. Delete the revision and carry on; the thing to fix is the habit of quoting the group on GitHub.
- **Tell the operator privately** what was exposed and where — never the value itself, and never in a comment here. They are e-mailed about this note as it is posted, so the report they need from you is the part the mail does not carry: what it was, and whether it has been rotated yet.

If this was not real — an example, a fixture, a shape being discussed — put it back with a value that reads as obviously synthetic (the word `fixture` inside it, or a run of repeated characters) and it will be left alone.

<sub>Posted automatically by the <code>issue-guards</code> workflow (.github/workflows/secret-redact.yml).</sub>
