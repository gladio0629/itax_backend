#!/usr/bin/env bash
# JWT uchun Ed25519 kalit juftligini yaratadi (faqat LOKAL ishlab chiqish uchun).
# Kalitlar secrets/jwt/ ga yoziladi; bu papka .gitignore da va repoga kirmaydi.
# Production'da kalitlar secret manager (Vault va h.k.) orqali beriladi.
set -euo pipefail

dir="$(cd "$(dirname "$0")/.." && pwd)/secrets/jwt"
mkdir -p "$dir"

if [[ -e "$dir/private.pem" ]]; then
  echo "kalit allaqachon bor: $dir/private.pem (o'chirmadim)" >&2
  exit 0
fi

openssl genpkey -algorithm ed25519 -out "$dir/private.pem"
openssl pkey -in "$dir/private.pem" -pubout -out "$dir/public.pem"

# Konteyner distroless'da "nonroot" (uid 65532) bilan ishlaydi va bind-mount egasi host foydalanuvchisi,
# shuning uchun lokalda fayllar o'qiladigan bo'lishi kerak. Faqat dev uchun!
chmod 755 "$dir"
chmod 644 "$dir/private.pem" "$dir/public.pem"
echo "yaratildi: $dir/{private,public}.pem"
