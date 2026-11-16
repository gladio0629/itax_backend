#!/usr/bin/env bash
# F-02 bosqich 2 uchun tayyorgarlik: dev CA va har servis uchun mTLS sertifikati (self-signed, FAQAT dev).
#
#   secrets/mtls/ca.crt, ca.key                     CA (ca.key 0600 — konteynerlarga ULANMAYDI)
#   secrets/mtls/<servis>/{tls.crt,tls.key,ca.crt}  CN=<servis>, SAN=DNS:<servis>; server+client auth; 825 kun
#
# Servislar ro'yxati — infra/mtls/policy.json, qo'shimcha nats va redis (server TLS). Idempotent: mavjud sertifikat qayta yaratilmaydi.
# Hozircha (bosqich 1) servislar sertifikatni ISHLATMAYDI — /run/secrets/mtls ga faqat ulangan.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
D=secrets/mtls
mkdir -p "$D"; chmod 0755 "$D"
if [[ ! -f $D/ca.key ]]; then
  (umask 077; openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$D/ca.key" 2>/dev/null)
  openssl req -x509 -new -key "$D/ca.key" -sha256 -days 3650 -subj "/O=IZGO dev/CN=IZGO dev internal CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" -addext "keyUsage=critical,keyCertSign,cRLSign" -out "$D/ca.crt"
  chmod 0644 "$D/ca.crt"
  echo "gen-mtls-certs: CA yaratildi" >&2
fi
# servislar (policy.json) + infra serverlari (NATS, Redis — TLS server sertifikati)
for s in $(python3 -c 'import json;print(" ".join(json.load(open("infra/mtls/policy.json"))["services"]))') nats redis; do
  sd="$D/$s"; mkdir -p "$sd"; chmod 0755 "$sd"
  [[ -f $sd/tls.crt ]] && continue
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$sd/tls.key" 2>/dev/null
  openssl req -new -key "$sd/tls.key" -subj "/O=IZGO dev/CN=$s" -out "$sd/tls.csr"
  openssl x509 -req -in "$sd/tls.csr" -CA "$D/ca.crt" -CAkey "$D/ca.key" -CAcreateserial -days 825 -sha256 \
    -extfile <(printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth,clientAuth\nkeyUsage=critical,digitalSignature\nbasicConstraints=CA:FALSE\n' "$s") \
    -out "$sd/tls.crt" 2>/dev/null
  rm -f "$sd/tls.csr"; cp "$D/ca.crt" "$sd/ca.crt"
  chmod 0644 "$sd/tls.crt" "$sd/ca.crt" "$sd/tls.key"   # konteyner foydalanuvchisi (nonroot) o'qishi uchun
  echo "gen-mtls-certs: $s sertifikati yaratildi" >&2
done
