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

# Stop service before swapping
systemctl stop read 2>/dev/null || true

# Swap binary and DB (clean WAL/SHM from local copy)
mv /opt/read-bin.new /opt/read-bin
mv /root/.read/read.db.new /root/.read/read.db
rm -f /root/.read/read.db-wal /root/.read/read.db-shm

# Systemd service (idempotent)
cat > /etc/systemd/system/read.service <<'SVC'
[Unit]
Description=read.ehrlich.dev
After=network.target

[Service]
Type=simple
ExecStart=/opt/read-bin --port 8080 --db /root/.read/read.db
Environment=HOME=/root
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable read
systemctl restart read
sleep 1
systemctl is-active read

# Nginx (idempotent)
cat > /etc/nginx/sites-enabled/read.ehrlich.dev.conf <<'NGX'
server {
    listen 80;
    listen [::]:80;

    server_name read.ehrlich.dev;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
NGX

nginx -t && systemctl reload nginx
echo "=== deployed ==="
REMOTE

echo ""
echo "done. site: http://read.ehrlich.dev"
