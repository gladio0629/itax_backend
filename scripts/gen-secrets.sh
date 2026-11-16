#!/usr/bin/env bash
# F-16 / F-25: Docker secrets. Sirlarni .env dan secrets/ ostidagi fayllarga ko'chiradi va yetishmaganlarini yaratadi.
#
#   secrets/postgres/POSTGRES_PASSWORD            postgres superuser (POSTGRES_PASSWORD_FILE)
#   secrets/postgres/<DB>_PASSWORD                har servis DB foydalanuvchisi (init-databases.sh, sync-passwords.sh)
#   secrets/svc/<servis>/<NOM>                    servis konteyneriga /run/secrets/izgo ga ulanadi (fayl nomi = env nomi)
#   secrets/external/<NOM>                        tashqi kalitlarning asl nusxasi (Hamkorbank — hali ishlatilmaydi;
#                                                 MAPBOX_TOKEN — pricing va matching-geo papkalariga nusxalanadi)
#
# Idempotent: mavjud fayl qayta yozilmaydi (aylantirish uchun faylni o'chiring va qayta ishga tushiring).
# .env dagi qiymat fayl yo'q bo'lganda bir marta ko'chiriladi, keyin .env da bo'shatiladi (zaxira: secrets/.env.bak-<vaqt>).
# Qiymatlar ekranga chiqarilmaydi. Egasi/huquqlari (faqat o'z konteyneri o'qiydi) — scripts/fix-secret-perms.sh
# (up.sh chaqiradi). secrets/ .gitignore da.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
umask 022

ENV_FILE=.env
envval() { # .env dagi qiymat (qo'shtirnoqsiz); yo'q bo'lsa bo'sh
  [[ -f $ENV_FILE ]] || return 0
  local line; line=$(grep -E "^$1=" "$ENV_FILE" | tail -n1 || true)
  line=${line#*=}; line=${line%\"}; line=${line#\"}; printf '%s' "$line"
}
rand() { openssl rand -hex "${1:-32}"; }

# put FILE VALUE — fayl yo'q bo'lsa yozadi (oxirida yangi qator YO'Q). Bo'sh qiymat — fayl yaratilmaydi.
put() {
  local f=$1 v=$2
  [[ -e $f ]] && return 0
  [[ -n $v ]] || return 0
  mkdir -p "$(dirname "$f")"
  printf '%s' "$v" > "$f"
  chmod 0600 "$f"   # yakuniy egasi/huquqi — scripts/fix-secret-perms.sh
  echo "gen-secrets: $f yaratildi" >&2
}
# from_env_or_rand FILE ENVNAME [bytes] — .env dagi qiymat, bo'lmasa tasodifiy
from_env_or_rand() { local v; v=$(envval "$2"); put "$1" "${v:-$(rand "${3:-32}")}"; }

mkdir -p secrets/postgres secrets/svc secrets/external

# --- postgres superuser: qiymati O'ZGARMASLIGI kerak (mavjud pg_data volume shu parol bilan yaratilgan)
if [[ ! -e secrets/postgres/POSTGRES_PASSWORD ]]; then
  pg=$(envval POSTGRES_PASSWORD)
  if [[ -z $pg ]]; then
    pg=$(rand 24)
    echo "gen-secrets: POSTGRES_PASSWORD yangi yaratildi — mavjud pg_data volume bo'lsa u eski parol bilan ishlaydi" >&2
  fi
  put secrets/postgres/POSTGRES_PASSWORD "$pg"
fi

# --- har servis DB paroli (F-25: endi superuser paroli emas)
declare -A DBSVC=([IDENTITY]=identity [DRIVER]=driver [TRIP_ORDER]=trip-order [PRICING]=pricing [WALLET]=wallet [BACKOFFICE]=backoffice)
for k in "${!DBSVC[@]}"; do
  from_env_or_rand "secrets/postgres/${k}_DB_PASSWORD" "${k}_DB_PASSWORD" 24
  # servis nusxasi har doim asl fayldan (aylantirishda eskirib qolmasin); keyin up.sh -> sync-passwords.sh
  src="secrets/postgres/${k}_DB_PASSWORD" dst="secrets/svc/${DBSVC[$k]}/DB_PASSWORD"
  if [[ ! -e $dst ]] || ! cmp -s "$src" "$dst"; then
    # atomar almashtirish: mavjud fayl 0440 (konteyner egasi) — ustidan yozib bo'lmaydi, papka esa guruhga yoziladi
    mkdir -p "$(dirname "$dst")"; cp "$src" "$dst.tmp" && mv -f "$dst.tmp" "$dst"; echo "gen-secrets: $dst yangilandi" >&2
  fi
done

# --- servislararo tokenlar (F-02 bosqich 1): har chaqiruvchiga o'z tokeni, infra/mtls/policy.json bo'yicha
"$ROOT/scripts/gen-internal-tokens.sh"
rm -f secrets/internal/SHARED_INTERNAL_TOKENS   # eski umumiy token (Etap 7 gacha) — endi ishlatilmaydi
# --- mTLS sertifikatlari (F-02 bosqich 2 uchun; hozir faqat konteynerlarga ulanadi)
"$ROOT/scripts/gen-mtls-certs.sh"
# --- NATS foydalanuvchilari va ruxsatlari (F-01; infra/nats/permissions.json)
"$ROOT/scripts/gen-nats-auth.sh"
# --- Redis ACL va parollar (F-25; infra/redis/acl.json)
"$ROOT/scripts/gen-redis-acl.sh"

# --- servisga xos sirlar
from_env_or_rand secrets/svc/driver/UPLOAD_SIGNING_SECRET UPLOAD_SIGNING_SECRET 32
for s in identity notification; do put "secrets/svc/$s/SMS_PROVIDER_KEY" "$(envval SMS_PROVIDER_KEY)"; done
put secrets/svc/identity/EMAIL_API_KEY "$(envval EMAIL_API_KEY)"

# --- tashqi kalitlar: asl nusxa secrets/external/ da (Hamkorbank — real provayder ulanganda servis papkasiga ko'chiriladi)
for k in HAMKORBANK_MERCHANT_KEY HAMKORBANK_MERCHANT_SECRET MAPBOX_TOKEN; do put "secrets/external/$k" "$(envval "$k")"; done

# --- Mapbox Directions (pricing, matching-geo): servis nusxasi har doim asl fayldan (aylantirishda eskirib qolmasin).
# Token yo'q bo'lsa nusxa ham yo'q — servislar Haversine bo'yicha ishlaydi.
for s in pricing matching-geo; do
  src=secrets/external/MAPBOX_TOKEN dst="secrets/svc/$s/MAPBOX_TOKEN"
  if [[ -s $src ]]; then
    if [[ ! -e $dst ]] || ! cmp -s "$src" "$dst"; then
      # atomar almashtirish (DB parollari kabi): mavjud fayl 0440 — ustidan yozib bo'lmaydi, papka esa guruhga yoziladi
      mkdir -p "$(dirname "$dst")"; cp "$src" "$dst.tmp" && mv -f "$dst.tmp" "$dst"; echo "gen-secrets: $dst yangilandi" >&2
    fi
  elif [[ -e $dst ]]; then
    rm -f "$dst"; echo "gen-secrets: $dst olib tashlandi (secrets/external/MAPBOX_TOKEN yo'q)" >&2
  fi
done

# --- .env dan maxfiy qiymatlarni olib tashlash (fayl allaqachon bor bo'lsa)
MOVED=(POSTGRES_PASSWORD IDENTITY_DB_PASSWORD DRIVER_DB_PASSWORD TRIP_ORDER_DB_PASSWORD PRICING_DB_PASSWORD WALLET_DB_PASSWORD
  BACKOFFICE_DB_PASSWORD INTERNAL_TOKENS UPLOAD_SIGNING_SECRET SMS_PROVIDER_KEY EMAIL_API_KEY
  HAMKORBANK_MERCHANT_KEY HAMKORBANK_MERCHANT_SECRET MAPBOX_TOKEN)
if [[ -f $ENV_FILE ]]; then
  dirty=0
  for k in "${MOVED[@]}"; do grep -Eq "^$k=.+" "$ENV_FILE" && dirty=1; done
  if (( dirty )); then
    bak="secrets/.env.bak-$(date +%Y%m%d-%H%M%S)"
    (umask 077; cp "$ENV_FILE" "$bak")
    for k in "${MOVED[@]}"; do sed -i "s|^$k=.*|$k=|" "$ENV_FILE"; done
    echo "gen-secrets: .env dagi maxfiy qiymatlar secrets/ ga ko'chirildi va bo'shatildi (zaxira: $bak, 0600)" >&2
  fi
fi
