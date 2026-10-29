#!/bin/sh
# Writes langfuse.env for compose.langfuse.yml: every secret generated here,
# none defaulted. Refuses to overwrite an existing file. The file is 0600 and
# holds the Langfuse project's key pair and admin password: treat it as a
# secret, and delete it with the volumes at teardown.
set -eu
out=${1:-langfuse.env}
[ ! -e "$out" ] || { echo "make-env: $out exists; remove it first" >&2; exit 1; }
hex() { openssl rand -hex "$1"; }
umask 077
cat > "$out" <<ENV
POSTGRES_PASSWORD=$(hex 24)
CLICKHOUSE_PASSWORD=$(hex 24)
MINIO_ROOT_PASSWORD=$(hex 24)
REDIS_AUTH=$(hex 24)
SALT=$(hex 32)
ENCRYPTION_KEY=$(hex 32)
NEXTAUTH_SECRET=$(hex 32)
LANGFUSE_PUBLIC_KEY=pk-lf-$(hex 16)
LANGFUSE_SECRET_KEY=sk-lf-$(hex 24)
LANGFUSE_ADMIN_PASSWORD=$(hex 16)
LANGFUSE_INIT_PROJECT_ID=${LANGFUSE_PROJECT:-spike}
ENV
echo "make-env: wrote $out (0600)" >&2
