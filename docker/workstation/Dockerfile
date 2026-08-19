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
FROM debian:bookworm-slim

# Packages loops rely on (git, make, curl) plus what the container itself needs:
# procps provides the pkill used to stop claude between wakes; the container's
# foreground process is `sleep infinity`, so sleep must be GNU coreutils, whose
# version accepts `infinity` where busybox's does not; curl, gnupg and
# ca-certificates fetch and verify the signing keys for the apt repos below.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg git make sudo procps \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI, installed from GitHub's own apt repo because Debian doesn't package
# it. The arch= field picks the build matching the image's architecture.
RUN install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
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
RUN curl -fsSL https://downloads.claude.ai/keys/claude-code.asc \
        -o /etc/apt/keyrings/claude-code.asc \
    && echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" \
        > /etc/apt/sources.list.d/claude-code.list \
    && apt-get update && apt-get install -y claude-code \
    && rm -rf /var/lib/apt/lists/*

# The unprivileged user loops run as. Its home, /home/loop, is where each loop's
# persistent volume is mounted. sudo is passwordless because loops run with no
# terminal attached and so cannot answer a password prompt when installing tools.
RUN useradd --create-home --shell /bin/bash loop \
    && echo 'loop ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/loop \
    && chmod 0440 /etc/sudoers.d/loop

# Turn off all non-essential network traffic — telemetry, error reporting, and
# update checks. Spool sends no telemetry.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

USER loop
WORKDIR /home/loop
