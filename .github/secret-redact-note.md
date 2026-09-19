**A credential shape was removed from this body.**

It looked like a token, key or password, so it was replaced with a marker. That is the fast part, and it is the smallest part — here is what it does **not** do:

- **The original is still readable.** GitHub keeps every revision of an edited body. Until you delete that revision — the `edited` menu above the body, then **Delete revision** — anyone who can see this page can still read what was there.
- **The value has already been published.** Treat it as compromised and **rotate it now**. Rotation is the part that actually closes this; the edit only stops the casual reader.
- Tell the operator privately what was exposed and where. Never the value itself, and never in a comment here.

If this was not a real credential — an example, a fixture, a shape being discussed — put it back with a value that reads as obviously synthetic (the word `fixture` inside it, or a run of repeated characters) and it will be left alone.

<sub>Posted automatically by the <code>secret-redact</code> workflow (.github/workflows/secret-redact.yml).</sub>
