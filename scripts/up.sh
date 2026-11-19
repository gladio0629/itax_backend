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

# Servis sirlari fayllarda (secrets/svc/<servis>/): hozircha identity review akkaunti (REVIEW_PHONES, REVIEW_OTP_CODE).
# .env dan o'qiydi; .env da bo'sh bo'lsa fayllarni o'chiradi. Xato bo'lsa (format) ishga tushirish to'xtaydi.
"$ROOT/scripts/gen-secrets.sh" "$ROOT/.env" "$ROOT/secrets"

# Avtomatik yaratiladigan maxfiy qiymatlar: faqat bo'sh (yoki 16 belgidan qisqa) bo'lsa to'ldiriladi.
# Qiymat mavjud qatorning O'ZIGA yoziladi — .env dagi tartib va izohlar buzilmaydi; boshqa o'zgaruvchilarga tegilmaydi.
fill_in_place() { # $1=nom $2=qiymat (faqat hex — sed uchun xavfsiz)
  if grep -q "^$1=" .env; then
    sed -i "s|^$1=.*|$1=$2|" .env
  else
    printf '%s=%s\n' "$1" "$2" >> .env
  fi
  echo "up.sh: .env ga $1 yozildi" >&2
}

# postgres superuser paroli. DIQQAT: pg_data volume allaqachon boshqa parol bilan yaratilgan bo'lsa, yangi parol
# ishlamaydi — u holda `docker compose down -v` (ma'lumot o'chadi) yoki eski parolni yozing.
grep -Eq '^POSTGRES_PASSWORD=.+' .env || fill_in_place POSTGRES_PASSWORD "$(openssl rand -hex 24)"
# driver: imzolangan yuklash URL lari uchun HMAC kaliti.
grep -Eq '^UPLOAD_SIGNING_SECRET=.{16,}' .env || fill_in_place UPLOAD_SIGNING_SECRET "$(openssl rand -hex 32)"
# servislararo umumiy sir (X-Internal-Token).
grep -Eq '^INTERNAL_TOKENS=.{16,}' .env || fill_in_place INTERNAL_TOKENS "$(openssl rand -hex 32)"

# compose POSTGRES_USER ni majburiy talab qiladi. .env ga yozilmaydi: bo'sh bo'lsa faqat shu ishga tushirish uchun izgo.
if ! grep -Eq '^POSTGRES_USER=.+' .env && [[ -z "${POSTGRES_USER:-}" ]]; then
  export POSTGRES_USER=izgo
  echo "up.sh: POSTGRES_USER bo'sh — izgo ishlatiladi (.env ga yozilmadi; doimiy qilish uchun .env ga yozing)" >&2
fi

# SMS / Email: rejim bo'sh bo'lsa compose va servis mock ishlatadi. real + bo'sh kalit ham mock — faqat ogohlantiramiz.
if grep -Eq '^SMS_MODE=real[[:space:]]*$' .env && ! grep -Eq '^SMS_PROVIDER_KEY=.+' .env; then
  echo "up.sh: OGOHLANTIRISH: SMS_MODE=real, lekin SMS_PROVIDER_KEY bo'sh — SMS mock rejimda ishlaydi" >&2
fi
if grep -Eq '^EMAIL_MODE=real[[:space:]]*$' .env && ! grep -Eq '^EMAIL_API_KEY=.+' .env; then
  echo "up.sh: OGOHLANTIRISH: EMAIL_MODE=real, lekin EMAIL_API_KEY bo'sh — email mock rejimda ishlaydi" >&2
fi

docker compose up -d --build "$@"
docker compose ps
