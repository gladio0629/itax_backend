#!/usr/bin/env bash
# Sir fayllari faqat o'z konteyneri foydalanuvchisiga o'qiladigan bo'lsin (0644 emas).
#
#   fayl:   <konteyner uid>:<host gid>  0440   — konteyner (egasi) o'qiydi, host'dagi skriptlar (guruh) o'qiydi
#   papka:  <konteyner uid>:<host gid>  0770   — host skriptlari yangi faylni yaratib/almashtira oladi (aylantirish)
#   faqat host'da ishlatiladiganlar (o'z tokenlari, CA kaliti, tashqi kalitlar, Redis admin paroli): <host uid> 0600/0700
#
# Konteyner uid'lari (Dockerfile USER / image): Go servislar distroless nonroot 65532; backoffice, driver — node 1000;
# wallet — app 10001; postgres 70; redis 999; nats — root (har qanday faylni o'qiydi, shuning uchun host 0600).
# Begona uid ga chown root talab qiladi — shuning uchun Docker konteyneri ichida bajariladi. Idempotent.
# scripts/up.sh gen-secrets.sh dan keyin chaqiradi. Uid o'zgarsa (Dockerfile USER), shu jadvalni yangilang.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HU=$(id -u) HG=$(id -g)
docker run --rm --user 0 --entrypoint sh -e HU="$HU" -e HG="$HG" -v "$ROOT/secrets:/s" redis:7-alpine -c '
set -eu
own() { # own UID PATH... — fayl 0440, papka 0770, egasi UID, guruhi host
  u=$1; shift
  for p in "$@"; do
    [ -e "$p" ] || continue
    if [ -d "$p" ]; then chown "$u:$HG" "$p"; chmod 0770 "$p"; find "$p" -type f -exec chown "$u:$HG" {} + -exec chmod 0440 {} +
    else chown "$u:$HG" "$p"; chmod 0440 "$p"; fi
  done
}
host_only() { for p in "$@"; do [ -e "$p" ] || continue
  if [ -d "$p" ]; then chown -R "$HU:$HG" "$p"; chmod 0700 "$p"; find "$p" -type f -exec chmod 0600 {} +
  else chown "$HU:$HG" "$p"; chmod 0600 "$p"; fi; done; }

chown "$HU:$HG" /s; chmod 0750 /s
for d in svc mtls; do [ -d /s/$d ] && { chown "$HU:$HG" /s/$d; chmod 0755 /s/$d; }; done
# servis sirlari: /run/secrets/izgo
for s in identity trip-order pricing matching-geo notification; do own 65532 /s/svc/$s; done
for s in backoffice driver; do own 1000 /s/svc/$s; done
own 10001 /s/svc/wallet
# JWT: private.pem faqat identity; public.pem ommaviy (bir nechta uid o'"'"'qiydi)
[ -f /s/jwt/private.pem ] && { chown 65532:$HG /s/jwt/private.pem; chmod 0440 /s/jwt/private.pem; }
[ -f /s/jwt/public.pem ] && { chown $HU:$HG /s/jwt/public.pem; chmod 0444 /s/jwt/public.pem; }
[ -d /s/jwt ] && { chown $HU:$HG /s/jwt; chmod 0755 /s/jwt; }
# postgres (entrypoint POSTGRES_PASSWORD_FILE ni postgres sifatida o'"'"'qiydi)
own 70 /s/postgres
# redis ACL fayli (redis-server uid 999); admin paroli faqat host
[ -f /s/redis/users.acl ] && { chown 999:$HG /s/redis/users.acl; chmod 0440 /s/redis/users.acl; }
[ -d /s/redis ] && { chown $HU:$HG /s/redis; chmod 0770 /s/redis; }
host_only /s/redis/ADMIN_PASSWORD
# nats (root) — auth.conf va tls kaliti host 0600 yetarli
host_only /s/nats
# mTLS: tls.key — o'"'"'z servisi; sertifikatlar va ca.crt ommaviy; CA kaliti faqat host
for s in identity trip-order pricing matching-geo notification; do [ -f /s/mtls/$s/tls.key ] && { chown 65532:$HG /s/mtls/$s/tls.key; chmod 0440 /s/mtls/$s/tls.key; }; done
for s in backoffice driver; do [ -f /s/mtls/$s/tls.key ] && { chown 1000:$HG /s/mtls/$s/tls.key; chmod 0440 /s/mtls/$s/tls.key; }; done
[ -f /s/mtls/wallet/tls.key ] && { chown 10001:$HG /s/mtls/wallet/tls.key; chmod 0440 /s/mtls/wallet/tls.key; }
[ -d /s/mtls/nats ] && host_only /s/mtls/nats && chmod 0755 /s/mtls/nats && chmod 0644 /s/mtls/nats/tls.crt /s/mtls/nats/ca.crt
[ -d /s/mtls/redis ] && host_only /s/mtls/redis
find /s/mtls -maxdepth 2 -name "*.crt" -not -path "*/nats/*" -not -path "*/redis/*" -exec chmod 0644 {} +
host_only /s/mtls/ca.key /s/mtls/ca.srl
host_only /s/internal /s/external
'
echo "fix-secret-perms: huquqlar qo'llandi" >&2
