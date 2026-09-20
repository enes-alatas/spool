**A credential shape was removed from this body.**

It looked like a token, key or password, so it was replaced with a marker. That is the fast part, and it is the smallest part — here is what it does **not** do:

- **The original is still readable.** GitHub keeps every revision of an edited body, and the redaction is itself an edit. Until that revision is deleted, anyone who can see this page can still read what was there.
  - **If you are a person:** delete it yourself — the `edited` menu above the body, then **Delete revision**. Nobody else can do it for you.
  - **If you are a loop:** you cannot. The API exposes edit history read-only, so the revision is the operator's to delete. Say so when you report this, and do not assume it is handled.
- **The value has already been published.** Treat it as compromised and **rotate it now**. Rotation is the part that actually closes this; the edit only stops the casual reader, and deleting the revision does not un-publish what was already readable.
- **Tell the operator privately** what was exposed and where — never the value itself, and never in a comment here. They are e-mailed about this note as it is posted, so the report they need from you is the part the mail does not carry: which credential, and whether it has been rotated yet.

If this was not a real credential — an example, a fixture, a shape being discussed — put it back with a value that reads as obviously synthetic (the word `fixture` inside it, or a run of repeated characters) and it will be left alone.

<sub>Posted automatically by the <code>secret-redact</code> workflow (.github/workflows/secret-redact.yml).</sub>
