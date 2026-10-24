#!/bin/sh
# Full state backup of the Compose stack into one directory: Sim's PostgreSQL dump and
# the data volumes. Secrets live in .env, which you must copy by a private channel.
# Usage: scripts/backup.sh [destination-dir]   (default: ./backups/<UTC timestamp>;
#        a relative destination is relative to the directory you run this from)
#
# workbench_data holds SQLite databases, so the workbench service is stopped while that
# volume is archived (a few seconds) and started again afterwards, even on failure.
# Set BACKUP_KEEP_RUNNING=1 to skip the stop (the archive may then be inconsistent).
#
# Restore (the dump was made with --clean --if-exists, so it replaces existing objects):
#   docker compose up -d db
#   gunzip -c sim_postgres.sql.gz | docker compose exec -T db psql -v ON_ERROR_STOP=1 -U postgres simstudio
#   docker run --rm -v agent-workbench_<volume>:/v -v "$PWD":/b alpine:3.21 tar xzf /b/<volume>.tgz -C /
set -eu

ALPINE_IMAGE=${ALPINE_IMAGE:-alpine:3.21}
start_dir=$PWD
cd "$(dirname "$0")/.."
root=$PWD

dest=${1:-backups/$(date -u +%Y%m%dT%H%M%SZ)}
case $dest in
	/*) ;;
	*) # A relative path given by the caller means relative to where they ran the script.
	   if [ $# -ge 1 ]; then dest=$start_dir/$dest; else dest=$root/$dest; fi ;;
esac
mkdir -p "$dest"
dest=$(cd "$dest" && pwd)
umask 077

# Dump to a temp file first: a failing pg_dump inside a pipe would otherwise be hidden
# by gzip's exit status and leave a truncated "backup".
tmp=$(mktemp "$dest/.sim_postgres.XXXXXX")
trap 'rm -f "$tmp"' EXIT
docker compose exec -T db pg_dump --clean --if-exists -U postgres simstudio > "$tmp"
[ -s "$tmp" ] || { echo "pg_dump produced an empty dump" >&2; exit 1; }
gzip -c "$tmp" > "$dest/sim_postgres.sql.gz"
gzip -t "$dest/sim_postgres.sql.gz"
[ -s "$dest/sim_postgres.sql.gz" ] || { echo "empty sim_postgres.sql.gz" >&2; exit 1; }
rm -f "$tmp"

restart_workbench=0
if [ "${BACKUP_KEEP_RUNNING:-0}" != 1 ] && [ -n "$(docker compose ps -q --status running workbench 2>/dev/null)" ]; then
	docker compose stop workbench
	restart_workbench=1
fi
trap '[ "$restart_workbench" = 1 ] && docker compose start workbench; rm -f "$tmp"' EXIT

for volume in workbench_data sim_uploads memu_embeddings caddy_data; do
	if docker volume inspect "agent-workbench_$volume" >/dev/null 2>&1; then
		docker run --rm -v "agent-workbench_$volume:/v:ro" -v "$dest:/b" "$ALPINE_IMAGE" tar czf "/b/$volume.tgz" -C / v
		gzip -t "$dest/$volume.tgz"
	fi
done
chmod 600 "$dest"/*
echo "backup written to $dest (copy .env separately; it holds the Sim encryption key)"
