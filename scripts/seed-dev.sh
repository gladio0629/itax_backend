#!/usr/bin/env bash
# Lokal dev test ma'lumotlari (FAQAT dev). Batafsil: docs/dev-testing.md
#   ./scripts/seed-dev.sh
# .env dan POSTGRES_USER va DEV_OTP_CODE o'qiladi (muhitda berilganlari ustun). Kod ekranga chiqarilmaydi.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
if [[ -f .env ]]; then
  while IFS='=' read -r key val; do
    [[ "$key" =~ ^(POSTGRES_USER|DEV_OTP_CODE|DEV_MODE|APP_ENV)$ ]] || continue
    val="${val%\"}"; val="${val#\"}"
    [[ -n "${!key:-}" ]] || export "$key=$val"
  done < <(grep -E '^[A-Z_]+=' .env)
fi
[[ "${DEV_MODE:-false}" == "true" ]] || { echo "XATO: .env da DEV_MODE=true emas — seed faqat dev rejimida (docs/dev-testing.md)" >&2; exit 1; }
[[ "${APP_ENV:-development}" != "production" ]] || { echo "XATO: APP_ENV=production" >&2; exit 1; }
exec python3 "$ROOT/scripts/seed_dev.py"
