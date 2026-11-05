#!/usr/bin/env bash
# Writes the env file docker/langfuse/compose.yml reads: every secret
# generated here and none defaulted (ADR-0064 item 5, #1299).
#
#   bash scripts/langfuse-setup.sh [--out FILE] [--admin-email EMAIL]
#
# FILE defaults to docker/langfuse/langfuse.env, which git ignores. It is
# written 0600 and never overwritten: it holds the project's key pair, the
# admin's password and every datastore's password, so treat it as a secret
# and delete it with the volumes when Langfuse is torn down.
#
# The profile joins the hub's egress network, which the hub's docker
# runtime makes at its first start, so the script refuses until it exists.
set -euo pipefail

out=docker/langfuse/langfuse.env
email=admin@example.com
network=${SPOOL_EGRESS_NETWORK:-spool-egress}

usage() { sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { printf 'langfuse-setup: %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case $1 in
	--out) out=${2:?}; shift 2 ;;
	--admin-email) email=${2:?}; shift 2 ;;
	-h | --help) usage ;;
	*) printf 'langfuse-setup: unknown argument %s\n' "$1" >&2; usage ;;
	esac
done

[ ! -e "$out" ] || die "$out exists; it holds the running Langfuse's secrets, so it is never overwritten (remove it only with the volumes)"
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
trap 'rm -f "$tmp"' EXIT
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
LANGFUSE_PUBLIC_KEY=pk-lf-$(hex 16)
LANGFUSE_SECRET_KEY=sk-lf-$(hex 24)
LANGFUSE_ADMIN_EMAIL=$email
LANGFUSE_ADMIN_PASSWORD=$(hex 16)
LANGFUSE_INIT_PROJECT_ID=spool
ENV
chmod 600 "$tmp"
# a link refuses: an existing name at $out is never replaced
ln "$tmp" "$out" 2>/dev/null || die "$out appeared while writing; left as it is"
printf 'langfuse-setup: wrote %s (0600). The admin is %s; its password and the project keys are in the file.\n' "$out" "$email" >&2
