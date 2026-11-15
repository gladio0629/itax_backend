#!/usr/bin/env bash
# IZGO backend'ni to'xtatadi. Ma'lumotlar (volume) SAQLANIB qoladi.
# Volume'larni ham o'chirish kerak bo'lsa, qo'lda: docker compose down -v
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

docker compose down "$@"
