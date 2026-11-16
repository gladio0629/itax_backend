#!/usr/bin/env bash
# Server uchun: Docker daemon darajasida log rotation (infra/docker/daemon.json) — barcha konteynerlar uchun standart.
# IZGO konteynerlari docker-compose.yml dagi `logging:` bilan bu skriptsiz ham aylantiriladi; bu skript serverdagi
# BOSHQA konteynerlarni ham qamraydi. root kerak:  sudo ./scripts/install-docker-logrotate.sh
# Mavjud /etc/docker/daemon.json ustidan yozilmaydi: log-driver/log-opts qo'shiladi (birlashtiriladi), zaxira olinadi.
# DIQQAT: oxirida Docker qayta ishga tushiriladi — barcha konteynerlar qisqa to'xtaydi (restart: unless-stopped qaytaradi).
# Yangi sozlama faqat YANGI yaratilgan konteynerlarga ta'sir qiladi (docker compose up -d --force-recreate).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ $EUID -eq 0 ]] || { echo "XATO: root kerak (sudo $0)" >&2; exit 1; }
DST=/etc/docker/daemon.json
mkdir -p /etc/docker
if [[ -f $DST ]]; then cp -a "$DST" "$DST.bak-$(date +%Y%m%d-%H%M%S)"; fi
python3 - "$DST" "$ROOT/infra/docker/daemon.json" <<'PY'
import json, os, sys
dst, src = sys.argv[1], sys.argv[2]
cur = json.load(open(dst)) if os.path.exists(dst) and os.path.getsize(dst) else {}
cur.update(json.load(open(src)))
tmp = dst + ".tmp"
with open(tmp, "w") as f: json.dump(cur, f, indent=2); f.write("\n")
os.replace(tmp, dst)
PY
echo "install-docker-logrotate: $DST yangilandi; Docker qayta ishga tushirilmoqda" >&2
systemctl restart docker
docker info --format 'logging driver: {{.LoggingDriver}}'
