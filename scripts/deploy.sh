#!/bin/bash
set -euo pipefail

# Deploy read to read.ehrlich.dev
# Usage: ./scripts/deploy.sh

HOST="${READ_DEPLOY_HOST:?Set READ_DEPLOY_HOST (e.g. root@1.2.3.4)}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"

echo "=== building linux/amd64 ==="
GOOS=linux GOARCH=amd64 go build -o /tmp/read-linux "$REPO/cmd/read"

echo "=== uploading binary ==="
scp /tmp/read-linux "$HOST:/opt/read-bin.new"

echo "=== uploading database ==="
sqlite3 ~/.read/read.db "PRAGMA wal_checkpoint(TRUNCATE);"
cp ~/.read/read.db /tmp/read-deploy.db
scp /tmp/read-deploy.db "$HOST:/root/.read/read.db.new"

echo "=== deploying on server ==="
ssh "$HOST" bash -s <<'REMOTE'
set -euo pipefail

chmod +x /opt/read-bin.new
mkdir -p /root/.read

# Stop service before swapping. Plumbing (read.service unit, nginx vhost) is
# owned by ~/repos/infra -- this script only ships the binary and DB.
systemctl stop read 2>/dev/null || true

# Swap binary and DB (clean WAL/SHM from local copy)
mv /opt/read-bin.new /opt/read-bin
mv /root/.read/read.db.new /root/.read/read.db
rm -f /root/.read/read.db-wal /root/.read/read.db-shm

systemctl restart read
sleep 1
systemctl is-active read
echo "=== deployed ==="
REMOTE

echo ""
echo "done. site: http://read.ehrlich.dev"
