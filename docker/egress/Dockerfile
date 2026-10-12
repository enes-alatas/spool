# The egress proxy image (ADR-0028): the one container that sits on both the
# workstations' internal network and the bridge, forwarding only what the
# allowlist permits.
#
# FROM scratch around a static binary — the proxy needs no shell, no package
# manager and no CA bundle (it tunnels TLS rather than terminating it), and a
# container with nothing in it is the right shape for the process guarding the
# fleet's only way out. Docker mounts /etc/resolv.conf itself, which is all
# Go's resolver needs.
#
# The build context is bin/ and the binary is cross-compiled by the Makefile,
# so TARGETARCH picks the right one and this one Dockerfile serves both the
# local `make image` and the multi-arch publishing build.
FROM scratch
# Links the published image to this repository on GitHub's registry (ADR-0052).
LABEL org.opencontainers.image.source="https://github.com/enes-alatas/spool"
ARG TARGETARCH
COPY spool-egress-${TARGETARCH} /spool-egress
EXPOSE 3128
ENTRYPOINT ["/spool-egress"]
