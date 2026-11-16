#!/usr/bin/env bash
# F-25: Redis ACL va parollar (infra/redis/acl.json).
#
#   secrets/svc/<servis>/REDIS_PASSWORD   servis paroli (yo'q bo'lsa yaratiladi; o'chirsangiz — aylantiriladi)
#   secrets/svc/<servis>/REDIS_URL        redis://<servis>:<parol>@redis:6379/0 — servis REDIS_URL ni shu fayldan oladi
#   secrets/redis/ADMIN_PASSWORD          "admin" foydalanuvchisi (faqat ops: redis-cli --user admin)
#   secrets/redis/users.acl               redis-server --aclfile; parollar SHA-256 xesh ko'rinishida; har safar qayta yoziladi
#
# "default" foydalanuvchi faqat PING (healthcheck) qila oladi, hech qanday kalitga kira olmaydi.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
python3 - <<'PY'
import hashlib, json, os, secrets
cfg = json.load(open("infra/redis/acl.json"))["users"]
def atomic_write(path, data):
    """0440 (konteyner egasi) faylni ustidan yozib bo'lmaydi — vaqtinchalik fayl + rename (papka guruhga yoziladi)."""
    tmp = path + ".tmp"
    old = os.umask(0o077)  # fix-secret-perms.sh gacha ham faqat egasi o'qisin
    try:
        with open(tmp, "w") as f: f.write(data)
    finally:
        os.umask(old)
    os.replace(tmp, path)
os.makedirs("secrets/redis", exist_ok=True)
def pw(path):
    if not os.path.exists(path):
        atomic_write(path, secrets.token_hex(24))
        print(f"gen-redis-acl: {path} yaratildi", flush=True)
    return open(path).read().strip()
def h(p): return "#" + hashlib.sha256(p.encode()).hexdigest()
def write_if_changed(path, data):
    if not os.path.exists(path) or open(path).read() != data:
        atomic_write(path, data)
        print(f"gen-redis-acl: {path} yozildi", flush=True)
# ACL faylida izoh qatori bo'lmaydi (redis rad etadi) — manba: infra/redis/acl.json
lines = [
    "user default on nopass resetkeys resetchannels -@all +ping",
    f"user admin on {h(pw('secrets/redis/ADMIN_PASSWORD'))} ~* &* +@all",
]
for name, u in cfg.items():
    d = f"secrets/svc/{name}"
    os.makedirs(d, exist_ok=True)
    p = pw(f"{d}/REDIS_PASSWORD")
    write_if_changed(f"{d}/REDIS_URL", f"redis://{name}:{p}@redis:6379/0")
    parts = ["user", name, "on", h(p), "resetkeys", "resetchannels"]
    parts += [f"~{k}" for k in u["keys"]] + [f"%R~{k}" for k in u["read_keys"]] + [f"&{c}" for c in u["channels"]]
    parts += ["+@all", "-@dangerous"] + u.get("extra_commands", [])
    lines.append(" ".join(parts))
write_if_changed("secrets/redis/users.acl", "\n".join(lines) + "\n")
PY
