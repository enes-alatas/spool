#!/usr/bin/env bash
# Writes the env file docker/langfuse/compose.yml reads: every secret
# generated here and none defaulted (ADR-0064 item 5, #1299). Beside it,
# the hub's own file: the project's key pair alone (#1388).
#
#   bash scripts/langfuse-setup.sh [--out FILE] [--hub-out FILE] [--admin-email EMAIL]
#
# FILE defaults to docker/langfuse/langfuse.env, which git ignores. It is
# written 0600 and never overwritten: it holds the project's key pair, the
# admin's password and every datastore's password, so treat it as a secret
# and delete it with the volumes when Langfuse is torn down. Keep it from
# the hub's user: every bare loop runs as that user.
#
# --hub-out defaults to docker/langfuse/hub.env, also ignored, 0600 and
# never overwritten. It holds the key pair alone, which is all the hub
# reads at start. Give it to the hub's user (chown) if that isn't you.
#
# The profile joins the hub's egress network, which the hub's docker
# runtime makes at its first start, so the script refuses until it exists.
set -euo pipefail

out=docker/langfuse/langfuse.env
hub_out=docker/langfuse/hub.env
email=admin@example.com
network=${SPOOL_EGRESS_NETWORK:-spool-egress}

usage() { sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { printf 'langfuse-setup: %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case $1 in
	--out) out=${2:?}; shift 2 ;;
	--hub-out) hub_out=${2:?}; shift 2 ;;
	--admin-email) email=${2:?}; shift 2 ;;
	-h | --help) usage ;;
	*) printf 'langfuse-setup: unknown argument %s\n' "$1" >&2; usage ;;
	esac
done

[ ! -e "$out" ] || die "$out exists; it holds the running Langfuse's secrets, so it is never overwritten (remove it only with the volumes)"
[ ! -e "$hub_out" ] || die "$hub_out exists; it holds the key pair the hub reads, so it is never overwritten (remove it with $out)"
case $email in
*@*.*) ;;
*) die "--admin-email $email isn't an email address" ;;
esac
command -v openssl >/dev/null || die "openssl generates the secrets and isn't installed"
command -v docker >/dev/null || die "docker isn't installed; the profile runs on docker"
docker network inspect "$network" >/dev/null 2>&1 ||
	die "the hub's egress network $network doesn't exist yet: start the hub with the docker runtime once, then run this again (or set SPOOL_EGRESS_NETWORK if its proxy image has another name)"

hex() { openssl rand -hex "$1"; }
umask 077
tmp=$(mktemp "$out.XXXXXX")
hub_tmp=$(mktemp "$hub_out.XXXXXX")
trap 'rm -f "$tmp" "$hub_tmp"' EXIT
public_key=pk-lf-$(hex 16)
secret_key=sk-lf-$(hex 24)
cat >"$tmp" <<ENV
# Written by scripts/langfuse-setup.sh. Secret: never commit or paste it.
SPOOL_EGRESS_NETWORK=$network
POSTGRES_PASSWORD=$(hex 24)
CLICKHOUSE_PASSWORD=$(hex 24)
MINIO_ROOT_PASSWORD=$(hex 24)
REDIS_AUTH=$(hex 24)
SALT=$(hex 32)
ENCRYPTION_KEY=$(hex 32)
NEXTAUTH_SECRET=$(hex 32)
LANGFUSE_PUBLIC_KEY=$public_key
LANGFUSE_SECRET_KEY=$secret_key
LANGFUSE_ADMIN_EMAIL=$email
LANGFUSE_ADMIN_PASSWORD=$(hex 16)
LANGFUSE_INIT_PROJECT_ID=spool
ENV
cat >"$hub_tmp" <<ENV
# Written by scripts/langfuse-setup.sh: what the hub reads at start
# (ADR-0064). Secret: never commit or paste it.
LANGFUSE_PUBLIC_KEY=$public_key
LANGFUSE_SECRET_KEY=$secret_key
ENV
chmod 600 "$tmp" "$hub_tmp"
# a link refuses: an existing name at either path is never replaced
ln "$tmp" "$out" 2>/dev/null || die "$out appeared while writing; left as it is"
ln "$hub_tmp" "$hub_out" 2>/dev/null ||
	die "$hub_out appeared while writing; left as it is. $out is written; copy its LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY lines into a 0600 file for the hub"
printf 'langfuse-setup: wrote %s and %s (both 0600). The admin is %s; its password and the project keys are in %s, the keys alone in %s.\n' \
	"$out" "$hub_out" "$email" "$out" "$hub_out" >&2
