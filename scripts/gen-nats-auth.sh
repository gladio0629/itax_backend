#!/usr/bin/env bash
# F-01: infra/nats/permissions.json dan NATS foydalanuvchilari va ruxsatlari.
#
#   secrets/svc/<servis>/NATS_PASSWORD   servis paroli (yo'q bo'lsa yaratiladi; o'chirsangiz — aylantiriladi)
#   secrets/nats/auth.conf               nats.conf ichiga include qilinadi (users + permissions); har safar qayta yoziladi
#
# Har foydalanuvchi: publish — faqat o'z hodisa mavzulari + JetStream API'ning o'ziga kerakli qismi (o'z stream'ini
# yaratish, o'qiydigan stream'larida consumer yaratish/ack); subscribe — faqat _INBOX.> (pub-ack va push consumer'lar
# shu yerga keladi). Boshqa servis mavzusiga yozish yoki to'g'ridan-to'g'ri obuna bo'lish — Permissions Violation.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
python3 - <<'PY'
import json, os, secrets
cfg = json.load(open("infra/nats/permissions.json"))
streams, users = cfg["streams"], cfg["users"]
def atomic_write(path, data):
    """0440 (konteyner egasi) faylni ustidan yozib bo'lmaydi — vaqtinchalik fayl + rename (papka guruhga yoziladi)."""
    tmp = path + ".tmp"
    old = os.umask(0o077)  # fix-secret-perms.sh gacha ham faqat egasi o'qisin
    try:
        with open(tmp, "w") as f: f.write(data)
    finally:
        os.umask(old)
    os.replace(tmp, path)
os.makedirs("secrets/nats", exist_ok=True)
def q(s): return '"' + s.replace('\\', '\\\\').replace('"', '\\"') + '"'
out = ["# AVTOMATIK (scripts/gen-nats-auth.sh, infra/nats/permissions.json). Qo'lda tahrirlamang.", "authorization {", "  users = ["]
for name, u in users.items():
    for s in u["streams"] + u["consumes"]:
        if s not in streams:
            raise SystemExit(f"permissions.json: {name}: noma'lum stream {s}")
    pdir = f"secrets/svc/{name}"
    os.makedirs(pdir, exist_ok=True)
    pf = f"{pdir}/NATS_PASSWORD"
    if not os.path.exists(pf):
        atomic_write(pf, secrets.token_hex(24))
        print(f"gen-nats-auth: {pf} yaratildi", flush=True)
    pw = open(pf).read().strip()
    pub = list(u["publish"]) + ["$JS.API.INFO", "$JS.API.STREAM.NAMES", "$JS.API.STREAM.INFO.*"]
    for s in u["streams"]:
        pub += [f"$JS.API.STREAM.CREATE.{s}", f"$JS.API.STREAM.UPDATE.{s}"]
    for s in u["consumes"]:
        pub += [f"$JS.API.CONSUMER.CREATE.{s}", f"$JS.API.CONSUMER.CREATE.{s}.>", f"$JS.API.CONSUMER.DURABLE.CREATE.{s}.>",
                f"$JS.API.CONSUMER.INFO.{s}.>", f"$JS.API.CONSUMER.MSG.NEXT.{s}.>", f"$JS.ACK.{s}.>", f"$JS.FC.{s}.>"]
    out.append(f"    {{ user: {q(name)}, password: {q(pw)}, permissions: {{")
    out.append(f"        publish: {{ allow: [{', '.join(q(p) for p in pub)}] }}")
    out.append(f"        subscribe: {{ allow: [\"_INBOX.>\"] }}")
    out.append("    } }")
out += ["  ]", "}", ""]
new = "\n".join(out)
p = "secrets/nats/auth.conf"
if not os.path.exists(p) or open(p).read() != new:
    atomic_write(p, new)
    print(f"gen-nats-auth: {p} yozildi ({len(users)} foydalanuvchi)", flush=True)
PY
