#!/usr/bin/env bash
# Tests scripts/langfuse-setup.sh and holds docker/langfuse/compose.yml to
# ADR-0064 item 5 (#1299). docker is a stand-in: the profile's own run is in
# OPERATIONS and the PR.
set -uo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0
pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
check() { if eval "$2"; then pass "$1"; else fail "$1"; fi; }

# a docker that knows one network, spool-egress
mkdir -p "$work/bin"
cat >"$work/bin/docker" <<'STUB'
#!/usr/bin/env bash
[ "$1 $2" = "network inspect" ] && [ "$3" = spool-egress ] && exit 0
exit 1
STUB
chmod +x "$work/bin/docker"
setup() { PATH=$work/bin:$PATH bash scripts/langfuse-setup.sh "$@"; }

check "refuses until the hub's egress network exists" \
	'SPOOL_EGRESS_NETWORK=other setup --out "$work/no-net.env" --hub-out "$work/no-net.hub" 2>"$work/err"; [ $? -ne 0 ] && [ ! -e "$work/no-net.env" ] && [ ! -e "$work/no-net.hub" ] && grep -q "start the hub" "$work/err"'
rm -f "$work/no-net.env"

setup --out "$work/a.env" --hub-out "$work/a.hub" --admin-email ops@example.org 2>/dev/null
check "writes the file 0600" '[ "$(stat -c %a "$work/a.env" 2>/dev/null || stat -f %Lp "$work/a.env")" = 600 ]'
keys="SPOOL_EGRESS_NETWORK POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD REDIS_AUTH SALT ENCRYPTION_KEY NEXTAUTH_SECRET LANGFUSE_PUBLIC_KEY LANGFUSE_SECRET_KEY LANGFUSE_ADMIN_EMAIL LANGFUSE_ADMIN_PASSWORD LANGFUSE_INIT_PROJECT_ID"
check "sets every variable compose requires, none empty" \
	'for k in $keys; do grep -Eq "^$k=.+" "$work/a.env" || exit 1; done'
check "every \${VAR:?} in the compose file is set by the env file" \
	'for k in $(grep -o "\${[A-Z_]*:?}" docker/langfuse/compose.yml | tr -d "\${}:?" | sort -u); do grep -q "^$k=" "$work/a.env" || { echo "missing $k"; exit 1; }; done'
check "the key pair has Langfuse's prefixes" \
	'grep -Eq "^LANGFUSE_PUBLIC_KEY=pk-lf-[0-9a-f]{32}$" "$work/a.env" && grep -Eq "^LANGFUSE_SECRET_KEY=sk-lf-[0-9a-f]{48}$" "$work/a.env"'
check "takes the admin email" 'grep -qx "LANGFUSE_ADMIN_EMAIL=ops@example.org" "$work/a.env"'
check "writes the hub's file 0600" '[ "$(stat -c %a "$work/a.hub" 2>/dev/null || stat -f %Lp "$work/a.hub")" = 600 ]'
check "the hub's file holds the key pair alone, the env file's" \
	'[ "$(grep -v "^#" "$work/a.hub")" = "$(grep -E "^LANGFUSE_(PUBLIC|SECRET)_KEY=" "$work/a.env")" ]'

setup --out "$work/b.env" --hub-out "$work/b.hub" 2>/dev/null
check "two runs generate different secrets" \
	'[ "$(grep ^SALT= "$work/a.env")" != "$(grep ^SALT= "$work/b.env")" ]'

cp "$work/a.env" "$work/a.before"
setup --out "$work/a.env" --hub-out "$work/a2.hub" 2>"$work/err"
check "never overwrites an existing file" '[ $? -ne 0 ] && cmp -s "$work/a.env" "$work/a.before" && [ ! -e "$work/a2.hub" ] && grep -q "never overwritten" "$work/err"'
cp "$work/a.hub" "$work/a.hub.before"
setup --out "$work/d.env" --hub-out "$work/a.hub" 2>"$work/err"
check "never overwrites an existing hub file, and writes neither" '[ $? -ne 0 ] && cmp -s "$work/a.hub" "$work/a.hub.before" && [ ! -e "$work/d.env" ] && grep -q "never overwritten" "$work/err"'
rm -f "$work/a.hub.before"
check "leaves no temporary file behind" '[ -z "$(ls "$work" | grep -E "\.(env|hub)\.")" ]'
check "refuses a malformed email" '! setup --out "$work/c.env" --hub-out "$work/c.hub" --admin-email nobody 2>/dev/null && [ ! -e "$work/c.env" ] && [ ! -e "$work/c.hub" ]'

check "git ignores the default env file" 'git check-ignore -q docker/langfuse/langfuse.env'
check "git ignores the default hub file" 'git check-ignore -q docker/langfuse/hub.env'

# The compose file, read as text: ADR-0064 item 5.
compose=docker/langfuse/compose.yml
services=$(grep -E '^  [a-z-]+:$' <(sed -n '/^services:/,/^[a-z]/p' "$compose") | tr -d ' :')
check "holds Langfuse's six containers" \
	'[ "$(echo $services)" = "langfuse-worker langfuse-web clickhouse minio redis postgres" ]'
check "every service is under the langfuse profile" \
	'[ "$(grep -c "^    profiles: \[langfuse\]$" "$compose")" = 6 ]'
check "every image is pinned by digest" \
	'! grep -E "^    image:" "$compose" | grep -vqE "@sha256:[0-9a-f]{64}$"'
check "one port is published, the UI's, on 127.0.0.1 by default" \
	'[ "$(grep -c "^    ports:" "$compose")" = 1 ] && grep -qF -- "- \${LANGFUSE_BIND:-127.0.0.1}:\${LANGFUSE_PORT:-3000}:80" "$compose"'
check "Langfuse's telemetry and sign-up are off" \
	'grep -q "TELEMETRY_ENABLED: \"false\"" "$compose" && grep -q "AUTH_DISABLE_SIGNUP: \"true\"" "$compose"'
check "the datastores and the worker are on the internal langfuse network alone" \
	'[ "$(grep -c "^    networks: \[langfuse\]$" "$compose")" = 5 ] && sed -n "/^networks:/,\$p" "$compose" | grep -A1 "^  langfuse:" | grep -q "internal: true"'
check "the egress network is external, named by a variable defaulting to spool-egress" \
	'sed -n "/^networks:/,\$p" "$compose" | grep -A3 "^  egress:" | grep -q "external: true" && grep -qF "name: \${SPOOL_EGRESS_NETWORK:-spool-egress}" "$compose"'
check "no service names a loop network" '! grep -q "spool-egress-loop" "$compose"'

if [ "$failures" -gt 0 ]; then
	printf '%d failed\n' "$failures"
	exit 1
fi
echo "all passed"
