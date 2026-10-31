# The default workstation image: a minimal, login-less sandbox that each loop's
# claude process runs inside. Loops install their own project toolchains at run
# time into their persistent /home/loop volume, so the image stays small and
# generic rather than trying to anticipate every stack.
#
# The Claude OAuth token is passed in as an environment variable at run time and
# is deliberately never baked in: an image is shared and cached, so a credential
# stored in it would leak to anyone who can pull or inspect it.
#
# Architecture-neutral: the base image and every apt repo below resolve the
# matching amd64 or arm64 build automatically, so one Dockerfile serves both.
# The base is pinned by its multi-arch index digest, so a release's image is
# built from a known base rather than whatever the tag points to that day.
# Dependabot moves the pin (.github/dependabot.yml).
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
# Links the published image to this repository on GitHub's registry (ADR-0052).
LABEL org.opencontainers.image.source="https://github.com/enes-alatas/spool"

# Packages loops rely on (git, make, curl) plus what the container itself needs:
# procps provides the pkill used to stop claude between wakes; the container's
# foreground process is `sleep infinity`, so sleep must be GNU coreutils, whose
# version accepts `infinity` where busybox's does not; curl, gnupg and
# ca-certificates fetch and verify the signing keys for the apt repos below.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg git make sudo procps \
    && rm -rf /var/lib/apt/lists/*

# Each apt repo's signing key is downloaded at build time, so the build checks
# the key's primary fingerprints against the ones its publisher documents, and
# stops on any other: a swapped key would otherwise vouch for a swapped package
# from the same origin. A publisher's key rotation is then a reviewed change
# here (#1200). The Claude Code key is pinned beside its own layer below.
ARG GH_KEY_FPRS="2C6106201985B60E6C7AC87323F3D4EA75716059 7F38BBB59D064DBCB3D84D725612B36462313325"

# GitHub CLI, installed from GitHub's own apt repo because Debian doesn't package
# it. The arch= field picks the build matching the image's architecture. Its two
# fingerprints are the ones https://github.com/cli/cli/blob/trunk/docs/install_linux.md lists.
RUN install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && fprs=$(gpg --show-keys --with-colons /etc/apt/keyrings/githubcli-archive-keyring.gpg \
        | awk -F: '$1=="pub"{p=1;next} p&&$1=="fpr"{print $10;p=0}' | sort | paste -sd' ') \
    && { [ "$fprs" = "$GH_KEY_FPRS" ] || { echo "GitHub CLI key fingerprints are '$fprs', not the pinned '$GH_KEY_FPRS'" >&2; exit 1; }; } \
    && chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Claude Code, from Anthropic's signed apt repo. This lands a self-contained
# native binary on the system PATH — deliberately not under /home/loop, which the
# loop's volume masks at run time — whose process name is exactly `claude`, so the
# `pkill -x claude` used to stop it between wakes matches (a Node-wrapper install
# would show up as `node` and be missed). Installed from apt, it does not try to
# self-update, so the version stays fixed until the image is rebuilt.
#
# The repo's `latest` channel, not `stable`: `latest` is what the host's native
# install tracks, so a contained loop runs the CLI the fleet's stream-json
# handling is verified against (TestedVersion). `stable` lags it.
#
# A rebuild must fetch the newest release, and Docker's cache would stop it: it
# keys this layer on the instruction's text, which a new upstream release does
# not change, so the old CLI would be reused (#304). CLAUDE_CODE_CACHEBUST is
# declared here, below the OS and gh layers, so a new value re-runs this install
# and everything after it and leaves the layers above cached. `make image`
# passes the time, so every rebuild installs whatever the repo serves then.
#
# A release doesn't take whatever the repo serves: it pins CLAUDE_CODE_VERSION
# to docker/workstation/claude-code.version, a version the fleet has run, so
# the image is tied to it and a bad upstream build waits for a reviewed bump
# (#1223, ADR-0052). The build checks that the CLI it installed is that one, and
# the image's label names it. Empty, as `make image` leaves it, means unpinned.
#
# The key's fingerprint is the one Claude Code's setup docs publish
# (https://code.claude.com/docs/en/setup).
ARG CLAUDE_CODE_KEY_FPR="31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE"
ARG CLAUDE_CODE_CACHEBUST
ARG CLAUDE_CODE_VERSION
LABEL dev.spool.claude-code.version="${CLAUDE_CODE_VERSION:-unpinned}"
RUN curl -fsSL https://downloads.claude.ai/keys/claude-code.asc \
        -o /etc/apt/keyrings/claude-code.asc \
    && fprs=$(gpg --show-keys --with-colons /etc/apt/keyrings/claude-code.asc \
        | awk -F: '$1=="pub"{p=1;next} p&&$1=="fpr"{print $10;p=0}' | sort | paste -sd' ') \
    && { [ "$fprs" = "$CLAUDE_CODE_KEY_FPR" ] || { echo "Claude Code key fingerprints are '$fprs', not the pinned '$CLAUDE_CODE_KEY_FPR'" >&2; exit 1; }; } \
    && echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/latest latest main" \
        > /etc/apt/sources.list.d/claude-code.list \
    && apt-get update \
    && apt-get install -y "claude-code${CLAUDE_CODE_VERSION:+=$CLAUDE_CODE_VERSION-*}" \
    && rm -rf /var/lib/apt/lists/* \
    && installed=$(claude --version | cut -d' ' -f1) \
    && { [ -z "$CLAUDE_CODE_VERSION" ] || [ "$installed" = "$CLAUDE_CODE_VERSION" ] \
        || { echo "Claude Code installed is $installed, not the pinned $CLAUDE_CODE_VERSION" >&2; exit 1; }; }

# The unprivileged user loops run as. Its home, /home/loop, is where each loop's
# persistent volume is mounted. sudo is passwordless because loops run with no
# terminal attached and so cannot answer a password prompt when installing tools.
RUN useradd --create-home --shell /bin/bash loop \
    && echo 'loop ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/loop \
    && chmod 0440 /etc/sudoers.d/loop

# The PreToolUse hook the hub pins on every loop's claude (ADR-0042), which
# refuses a tool call that breaks a mechanical fleet rule. `make image` builds
# it for the image's architecture into bin/ first. Owned by root and outside
# the loop's volume, though the loop's sudo means it guards against slips, not
# a loop set on getting past it.
ARG TARGETARCH
COPY bin/spool-hook-${TARGETARCH} /usr/local/bin/spool-hook

# spool-github (#1054): git's credential helper for github.com, and, as the gh
# link in /usr/local/bin ahead of /usr/bin/gh, the gh that sets GH_TOKEN. Both
# ask the hub for a fresh token of the github-app the loop holds; for a loop
# holding none the hub hands no route, and gh passes straight through.
COPY bin/spool-github-${TARGETARCH} /usr/local/bin/spool-github
RUN ln -s spool-github /usr/local/bin/gh

# Turn off all non-essential network traffic — telemetry, error reporting, and
# update checks. Spool sends no telemetry.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

USER loop
WORKDIR /home/loop
