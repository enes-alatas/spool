---
title: From a release
description: "Run a released build: the binary and images, with nothing to compile."
weight: 2
---

A release carries the built binary and the two images, so this replaces step 1. You need Docker running, `curl` and Claude Code. Run this in a new terminal:

```bash
v=$(curl -fsSLo /dev/null -w '%{url_effective}' https://github.com/enes-alatas/spool/releases/latest); v=${v##*/}
p=$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')   # this machine's: linux_amd64, linux_arm64, darwin_arm64 or darwin_amd64
mkdir -p ~/spool/bin && cd ~/spool
base=https://github.com/enes-alatas/spool/releases/download/$v
curl -fsSLO "$base/spool_${v}_${p}.tar.gz" -O "$base/SHA256SUMS"
grep " spool_${v}_${p}.tar.gz\$" SHA256SUMS | $(command -v shasum >/dev/null && echo shasum -a 256 || echo sha256sum) -c   # the file is the release's
tar -xzf "spool_${v}_${p}.tar.gz" && install -m 755 "spool_${v}_${p}"/spool* bin/
./bin/spool images                           # read it: two pulls by digest, two tags
./bin/spool images | sh
```

For the release's own README, open the address this prints: `echo "https://github.com/enes-alatas/spool/blob/$v/README.md"`.
Then go on from [step 2](../quick-start/) of the quick start with `./bin/spool`. If you have `gh`, you can also check that the release workflow built the file:
`gh attestation verify "spool_${v}_${p}.tar.gz" --repo enes-alatas/spool`. To upgrade later, see [Upgrade](../../run-a-hub/upgrade/).
