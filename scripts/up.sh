#!/usr/bin/env bash
# IZGO backend'ni ishga tushiradi (infra + barcha servislar).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ ! -f .env ]]; then
  echo "XATO: .env topilmadi. Avval: cp .env.example .env  (bo'sh qiymatlar uchun default ishlaydi; maxfiylarni up.sh o'zi yaratadi)." >&2
  exit 1
fi

# identity JWT kalitlari (Ed25519). Bo'lmasa servis ishga tushmaydi; docker esa yo'q bind-mount
# manbasini root nomidan papka qilib yaratib qo'yadi, shuning uchun kalitlarni OLDINDAN yaratamiz.
if [[ ! -f secrets/jwt/private.pem || ! -f secrets/jwt/public.pem ]]; then
  "$ROOT/scripts/gen-jwt-keys.sh"
fi

# F-16: maxfiy qiymatlar .env da emas, secrets/ ostidagi fayllarda (Docker secrets). Yetishmaganlarini yaratadi,
# .env da qolgan eski qiymatlarni fayllarga ko'chirib, .env da bo'shatadi (zaxira bilan).
"$ROOT/scripts/gen-secrets.sh"

# compose POSTGRES_USER ni majburiy talab qiladi. .env ga yozilmaydi: bo'sh bo'lsa faqat shu ishga tushirish uchun izgo.
if ! grep -Eq '^POSTGRES_USER=.+' .env && [[ -z "${POSTGRES_USER:-}" ]]; then
  export POSTGRES_USER=izgo
  echo "up.sh: POSTGRES_USER bo'sh — izgo ishlatiladi (.env ga yozilmadi; doimiy qilish uchun .env ga yozing)" >&2
fi

# SMS / Email: rejim bo'sh bo'lsa compose va servis mock ishlatadi. real + bo'sh kalit ham mock — faqat ogohlantiramiz.
if grep -Eq '^SMS_MODE=real[[:space:]]*$' .env && [[ ! -s secrets/svc/identity/SMS_PROVIDER_KEY ]]; then
  echo "up.sh: OGOHLANTIRISH: SMS_MODE=real, lekin SMS_PROVIDER_KEY bo'sh — SMS mock rejimda ishlaydi" >&2
fi
if grep -Eq '^EMAIL_MODE=real[[:space:]]*$' .env && [[ ! -s secrets/svc/identity/EMAIL_API_KEY ]]; then
  echo "up.sh: OGOHLANTIRISH: EMAIL_MODE=real, lekin EMAIL_API_KEY bo'sh — email mock rejimda ishlaydi" >&2
fi

# F-25: mavjud pg_data volume uchun servis parollarini secrets/postgres ga moslash (init skript faqat birinchi marta ishlaydi)
docker compose up -d --wait postgres
docker compose exec -T postgres /usr/local/bin/izgo-sync-passwords.sh

docker compose up -d --build "$@"
docker compose ps
