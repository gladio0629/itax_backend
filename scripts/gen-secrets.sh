#!/usr/bin/env bash
# .env dagi servis sirlarini fayllarga yozadi: secrets/svc/<servis>/<NOM>. Compose bu kataloglarni konteynerga
# mount qiladi; servis qiymatni env'dan emas, fayldan o'qiydi (docker inspect / env da ko'rinmaydi).
# up.sh har ishga tushishda chaqiradi. Qiymatlar ekranga chiqarilmaydi.
#
# Hozircha: identity uchun REVIEW_PHONES va REVIEW_OTP_CODE (App Store / Google Play tekshiruvchisi akkaunti).
#   ikkalasi bor   -> fayllar yoziladi (o'zgarmagan bo'lsa tegilmaydi)
#   ikkalasi bo'sh -> fayllar O'CHIRILADI (aks holda eski doimiy kod jim ishlab turaverardi)
#   bittasi bo'sh yoki format noto'g'ri -> xato, hech narsa yozilmaydi
#
# Ishlatish: scripts/gen-secrets.sh [ENV_FAYL] [SECRETS_KATALOG]   (default: .env va secrets)
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${1:-$ROOT/.env}"
SECRETS_DIR="${2:-$ROOT/secrets}"
# distroless nonroot (services/identity/Dockerfile: USER nonroot)
SVC_UID=65532

[[ -f "$ENV_FILE" ]] || { echo "gen-secrets.sh: XATO: $ENV_FILE topilmadi" >&2; exit 1; }

# env_get NOM — .env dan qiymat (source qilinmaydi): oxirgi `NOM=` qatori, CR, chetdagi bo'shliq va qo'shtirnoq olinadi.
env_get() {
  local line v
  line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?$1=" "$ENV_FILE" | tail -n 1 || true)"
  v="${line#*=}"
  v="${v//$'\r'/}"
  v="${v#"${v%%[![:space:]]*}"}"; v="${v%"${v##*[![:space:]]}"}"
  if [[ ${#v} -ge 2 && ( ( "${v:0:1}" == '"' && "${v: -1}" == '"' ) || ( "${v:0:1}" == "'" && "${v: -1}" == "'" ) ) ]]; then
    v="${v:1:${#v}-2}"
  fi
  printf '%s' "$v"
}

# ensure_dir KATALOG — yo'q bo'lsa yaratadi. secrets/ ning o'zi boshqa host foydalanuvchilariga yopiq bo'lsin.
ensure_dir() {
  if [[ ! -d "$SECRETS_DIR" ]]; then
    mkdir -p "$SECRETS_DIR" && chmod 0750 "$SECRETS_DIR"
  fi
  if [[ ! -d "$1" ]]; then
    mkdir -p "$1"
    if [[ $EUID -eq 0 ]]; then
      chown "$SVC_UID:$SVC_UID" "$1" && chmod 0750 "$1"
    else
      # konteyner foydalanuvchisi (65532) katalogdan o'tishi kerak; host'da secrets/ (0750) himoya qiladi
      chmod 0755 "$1"
    fi
  fi
}

# write_secret KATALOG NOM QIYMAT — atomik yozish (temp + mv); qiymat o'zgarmagan bo'lsa tegilmaydi.
write_secret() {
  local dir="$1" name="$2" val="$3" dst="$1/$2" tmp
  if [[ -f "$dst" && -r "$dst" ]] && [[ "$(cat "$dst")" == "$val" ]]; then
    return 0
  fi
  tmp="$(mktemp "$dir/.$name.XXXXXX")"
  printf '%s\n' "$val" > "$tmp"
  if [[ $EUID -eq 0 ]]; then
    chown "$SVC_UID:$SVC_UID" "$tmp" && chmod 0400 "$tmp"
  else
    # root bo'lmasak egani 65532 ga o'zgartira olmaymiz; konteyner o'qishi uchun 0444.
    # Host'da boshqa foydalanuvchilar secrets/ (0750) orqali yetib bora olmaydi.
    chmod 0444 "$tmp"
  fi
  mv -f "$tmp" "$dst"
  echo "gen-secrets.sh: $dst yozildi" >&2
}

remove_secret() {
  if [[ -e "$1/$2" ]]; then
    rm -f "$1/$2"
    echo "gen-secrets.sh: $1/$2 o'chirildi (.env da qiymat yo'q)" >&2
  fi
}

# ---------- identity: review akkaunti ----------
id_dir="$SECRETS_DIR/svc/identity"
review_code="$(env_get REVIEW_OTP_CODE)"
review_phones="$(env_get REVIEW_PHONES)"
review_phones="${review_phones//[[:space:]]/}"   # "+998..., +998..." -> "+998...,+998..."
review_phones="$(printf '%s' "$review_phones" | sed -E 's/,+/,/g; s/^,//; s/,$//')"

if [[ -z "$review_code" && -z "$review_phones" ]]; then
  if [[ -d "$id_dir" ]]; then
    remove_secret "$id_dir" REVIEW_OTP_CODE
    remove_secret "$id_dir" REVIEW_PHONES
  fi
else
  if [[ -z "$review_code" || -z "$review_phones" ]]; then
    echo "gen-secrets.sh: XATO: REVIEW_OTP_CODE va REVIEW_PHONES .env da birga berilishi kerak" >&2
    exit 1
  fi
  if [[ ! "$review_code" =~ ^[0-9]{6}$ ]]; then
    echo "gen-secrets.sh: XATO: REVIEW_OTP_CODE aynan 6 ta raqam bo'lishi kerak" >&2
    exit 1
  fi
  IFS=',' read -r -a _phones <<< "$review_phones"
  for p in "${_phones[@]}"; do
    if [[ ! "$p" =~ ^\+[1-9][0-9]{7,14}$ ]]; then
      echo "gen-secrets.sh: XATO: REVIEW_PHONES: har bir qiymat E.164 formatida bo'lishi kerak (+998901234567)" >&2
      exit 1
    fi
  done
  ensure_dir "$id_dir"
  write_secret "$id_dir" REVIEW_OTP_CODE "$review_code"
  write_secret "$id_dir" REVIEW_PHONES "$review_phones"
fi
