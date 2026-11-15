# ADR-0071: The operator docs are a Hugo site on GitHub Pages

Date: 2026-11-15 · Status: accepted (the operator chose Hugo with the Hextra theme on 2026-11-15, in direct messages to Elliot, after seeing three of Spool's pages built with it and with Astro Starlight, and accepted the rest of this ADR as proposed the same day; recorded on #1613) · Relates to: ADR-0052

## Context

Spool's documentation is a 189-line README and a 1,300-line
OPERATIONS.md, with CONTAINMENT, TELEGRAM and SLACK beside them. The
README is pitch, install guide, feature tour, security summary, flag
reference and contributor guide at once. An operator looking for one
task scrolls a page that also holds every other task. The operator
asked for a documentation site (#1612).

The site is published to strangers, so it has to settle five questions:
1. What builds it, and what that costs CI to keep up.
2. Where it is hosted.
3. Which version of Spool it describes.
4. What moves out of the repository's files.
5. What happens to the links that point at today's files.

Four constraints shape the answer:
- **Shipped builds link to today's files.** v0.8.0's control room links to
  `https://github.com/enes-alatas/spool/blob/main/docs/OPERATIONS.md#langfuse-beside-the-hub`,
  `…#a-tailscale-funnel-beside-caddy` and `…/docs/TELEGRAM.md`. `spool upgrade`'s
  messages name `docs/OPERATIONS.md` and a section. A binary keeps
  those links for as long as anyone runs it.
- **The README on main runs ahead of the latest release.** The timed
  stranger's run found main's README describing v0.9.0's channel page
  to someone who had installed v0.8.0 (#1607). A site that follows only
  main would repeat that for every reader.
- **Every dependency is a supply-chain cost.** It needs a lock, alerts
  and bumps. web/ already carries one npm tree.
- **The site is a page strangers load.** It must not track them or load
  code from another origin.

Three generators were weighed. Each was built from the same three pages
of Spool's docs on a fresh runner:
- **Hugo with the Hextra theme.** One binary, built in 0.2s. Search, a
  light and dark toggle, a page outline and a version menu come with
  the theme.
- **Astro Starlight.** The most polished look by a small margin. It built
  in 1.4s with 274 npm packages, and Astro and Starlight release new
  majors often. Its versioning plugin calls itself early-stage.
- **Material for MkDocs or Zensical.** Material for MkDocs is in maintenance
  until 2027-05-05 and Zensical is at 0.0.x. Either would bring a Python
  toolchain into CI.

## Decision

1. **Hugo builds the site, with the Hextra theme.**
   - **The pin.** A file in `site/` pins the Hugo version and its SHA-256.
     CI downloads that binary and checks it before running it.
   - **Each tree builds with its own Hugo.** Every build reads the pin
     beside the content it builds, whichever workflow runs it. A
     release's site is then always built with the Hugo its theme was
     tested against, and a bump on main never breaks an older release's
     build.
   - **The theme.** It is vendored into the repository at a release tag,
     with the tag and commit recorded beside it. There is no npm, Python
     or Hugo module fetch at build time, so nothing new needs Dependabot.
   - **Bumps.** A Hugo or theme bump is a chore PR that names the release
     it moves to.

2. **The site's source is `site/`.** It holds the Hugo config, the
   vendored theme and the pages under `site/content/`. Pages are plain
   markdown, so a page reads correctly on GitHub too. Hextra shortcodes
   are allowed only where markdown cannot say it.

3. **GitHub Pages hosts it, deployed from Actions.** The address is
   `https://enes-alatas.github.io/spool/`. There is no `gh-pages` branch.
   - **The workflow.** One workflow builds and deploys. It runs on a
     published release, on a push to main that changes `site/`, and by
     hand. Only its deploy job holds `pages: write` and `id-token: write`;
     the build job reads.
   - **What the operator turns on.** Pages, in the repository's settings
     (Source: GitHub Actions), and any custom domain later. Both are
     outward-facing and his to approve in words.

4. **The site follows the latest release, and main lives under `/next/`.**
   - **The root.** Each deploy builds the root from the latest release's
     tag.
   - **`/next/`.** Built from main, with a banner on every page saying it
     describes unreleased work.
   - **Older releases.** Not built; the version menu links each one to its
     tag's files on GitHub.
   - **Until a release carries `site/`.** The root is built from main and
     carries the same banner.
   - **A failed build.** The root and `/next/` build as separate steps, so
     a red run names which one failed. A failure in either stops the
     deploy, and the live site stays as it was. Since each tree builds
     with its own pin, the root fails only on a fault in that release's
     own `site/`. A fault there is fixed by a patch release.

   A reader who installed the latest release reads what it does, the
   mismatch of #1607 can't happen at the root, and a release needs no step
   of its own to publish its docs.

5. **Operator docs move to the site; contributor docs stay as files.**
   - **Moves:** OPERATIONS, CONTAINMENT, TELEGRAM and SLACK, and the
     README's guide parts: the quick start's detail, From a release, How
     it works, Teams, Where loops run, Security in brief, Chat surfaces,
     Who can talk to your loops and Flags.
   - **Stays:** ARCHITECTURE, CONVENTIONS, QUALITY, THREAT-MODEL, VISION,
     the ADRs, AGENTS, CONTRIBUTING and SECURITY.
   - **The README.** It keeps the pitch, the video and a short quick start,
     and links to the site.

6. **Every moved file stays as a stub with its old headings.** A file the
   site replaces is cut to its title, a link to the site, and every `##`
   and `###` heading it had, each followed by one line linking to that
   section's new page. An old anchor, from a shipped binary, a release note
   or another doc, still lands on its heading and is one click from the
   answer. Stubs stay until no supported release links to them; that is
   decided in a later ADR, not here. New builds of the control room and
   the CLI link to the site.

7. **The site loads nothing from another origin.**
   - **Off:** no analytics, no web fonts or scripts from a CDN, no
     comment widget.
   - **On:** search runs on an index built with the site.
   - **Checked:** the PR that builds the site checks the output for any
     `src` or `href` that loads from another origin. A link a reader
     follows is not a load.

8. **The page map.** The split produces these pages. Each line is a
   child of the epic, or part of one:

   | Section | Pages |
   |---|---|
   | Start | Quick start · From a release · Your first loop · Chat surfaces (Telegram, Slack) |
   | Guides | Teams and channels · Who can talk to your loops · Connections · Cloud loops · Claude logins · Management tokens · Langfuse beside the hub |
   | Run a hub | Run it as a service (user, unit, TLS with Caddy, firewall, cloud listener, Tailscale Funnel) · Back up and restore · Upgrade (with `spool upgrade`, by hand, rolling back) |
   | Security | What contains a loop · Security in brief |
   | Reference | Flags · Settings · Release-specific steps |

## Consequences

- An operator finds a task by name in the navigation or by search. The
  release they run is described at the root, and main's changes wait at
  `/next/`.
- CI gains one workflow and one pinned binary, and no package tree.
  Keeping Hugo and Hextra current is a hand-made chore. Nothing alerts
  on a Hugo security fix the way Dependabot alerts on a module.
- Docs of record still change in the same PR as the code they describe
  (CONVENTIONS.md). For an operator page, that file is now under
  `site/content/`.
- A doc's link to an operator page points at the site. A doc's link to
  a contributor file stays a repository path.
- The release skill's "Release-specific steps" lives on a site page. The
  skill is updated in the child that moves it.
- The stubs keep links that already shipped working for as long as they
  stay. Removing one is a decision of its own.
