# The official spool-workstation image (ADR-0017 §7, ADR-0018 §7): a minimal,
# login-less workstation. Loops sudo-install their own project toolchains into
# the persistent /home/loop volume. Auth (CLAUDE_CODE_OAUTH_TOKEN) is injected
# per exec by the runtime (#11) and is never baked here.
#
# Arch-neutral: debian and the apt repos below resolve the correct amd64/arm64
# binaries. `make image` builds the host arch; `make image-multiarch` both.
FROM debian:bookworm-slim

# ADR-0017 §7 toolchain + the plumbing the runtime relies on: procps for
# `pkill -x claude`, coreutils `sleep infinity` as PID 1 (base image; busybox's
# sleep lacks `infinity`), sh, sudo, and the apt-signing tools the repos below need.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg git make sudo procps \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI (`gh`) from GitHub's official apt repo — not in debian main.
RUN install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && chmod 0644 /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Claude Code from Anthropic's official signed apt repo (stable channel). Installs
# the native binary (process name `claude`, on PATH under /usr, i.e. OUTSIDE the
# volume-masked /home/loop) and pulls ripgrep; apt installs don't auto-update.
RUN curl -fsSL https://downloads.claude.ai/keys/claude-code.asc \
        -o /etc/apt/keyrings/claude-code.asc \
    && echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" \
        > /etc/apt/sources.list.d/claude-code.list \
    && apt-get update && apt-get install -y claude-code \
    && rm -rf /var/lib/apt/lists/*

# Non-root `loop` user; home /home/loop is the volume mount target (ADR-0018).
# No TTY is ever allocated (the runtime execs with -i only), so sudo must not need one.
RUN useradd --create-home --shell /bin/bash loop \
    && echo 'loop ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/loop \
    && chmod 0440 /etc/sudoers.d/loop

# No telemetry, ever (CLAUDE.md). The catch-all also suppresses update checks.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

USER loop
WORKDIR /home/loop
