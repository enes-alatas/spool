# The tier-2 workstation image: fakeclaude standing in as claude, following
# the ADR-0018 image convention — user `loop` with home /home/loop, `claude`
# on PATH as a real binary whose process name is claude, sh + coreutils
# sleep + procps present. Build context is bin/ (make itest builds it).
FROM debian:bookworm-slim
RUN useradd --create-home --shell /bin/bash loop \
    && apt-get update \
    && apt-get install -y --no-install-recommends procps \
    && rm -rf /var/lib/apt/lists/*
COPY fakeclaude /usr/local/bin/claude
ENV FAKECLAUDE_STATE=/home/loop/.fakeclaude-state
USER loop
WORKDIR /home/loop
