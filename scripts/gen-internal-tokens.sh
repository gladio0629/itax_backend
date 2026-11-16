#!/usr/bin/env bash
# F-02 bosqich 1: har servisga O'Z ichki tokeni va infra/mtls/policy.json bo'yicha INTERNAL_TOKENS fayli.
#
#   secrets/internal/<servis>.token          servisning o'z tokeni (yo'q bo'lsa yaratiladi; o'chirsangiz — aylantiriladi)
#   secrets/svc/<servis>/INTERNAL_TOKENS     "<o'z tokeni>,<ruxsat etilgan chaqiruvchilar tokenlari...>"
#
# Servislar ro'yxatdagi BIRINCHI tokenni chiqishda yuboradi va ro'yxatdagi istalganini qabul qiladi — shuning uchun
# A servis B ni faqat policy'da B.callers ichida A bo'lsa chaqira oladi. INTERNAL_TOKENS har safar policy'dan qayta
# yoziladi (policy o'zgarsa — servislarni qayta ishga tushiring). Qiymatlar ekranga chiqarilmaydi.
# Cheklov (bosqich 1): chaqiriluvchi o'z chaqiruvchilari tokenini biladi; bosqich 2 da mTLS (gen-mtls-certs.sh).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
umask 022
mkdir -p secrets/internal secrets/svc
python3 - <<'PY'
import json, os, secrets
pol = json.load(open("infra/mtls/policy.json"))["services"]
def atomic_write(path, data):
    """0440 (konteyner egasi) faylni ustidan yozib bo'lmaydi — vaqtinchalik fayl + rename (papka guruhga yoziladi)."""
    tmp = path + ".tmp"
    old = os.umask(0o077)  # fix-secret-perms.sh gacha ham faqat egasi o'qisin
    try:
        with open(tmp, "w") as f: f.write(data)
    finally:
        os.umask(old)
    os.replace(tmp, path)
def tok(s):
    p = f"secrets/internal/{s}.token"
    if not os.path.exists(p):
        atomic_write(p, secrets.token_hex(32))
        print(f"gen-internal-tokens: {p} yaratildi", flush=True)
    return open(p).read().strip()
for s in pol:
    for c in pol[s]["callers"]:
        if c not in pol:
            raise SystemExit(f"policy: {s}.callers ichida noma'lum servis {c}")
for s, v in pol.items():
    vals = [tok(s)] + [tok(c) for c in v["callers"]]
    d = f"secrets/svc/{s}"
    os.makedirs(d, exist_ok=True)
    p = f"{d}/INTERNAL_TOKENS"
    new = ",".join(vals)
    if not os.path.exists(p) or open(p).read() != new:
        atomic_write(p, new)
        print(f"gen-internal-tokens: {p} <- o'zi + {v['callers'] or 'chaqiruvchi yo`q'}", flush=True)
PY
