#!/usr/bin/env python3
"""Lokal ishlab chiqish uchun test ma'lumotlari (FAQAT dev; productionda ishlatilmaydi).

Hammasi HAQIQIY oqimlar orqali (Kong -> servislar), shuning uchun ID lar barcha bazalarda mos:
  - shaharlar, mashhur yo'nalishlar, tarif — migratsiyalarda bor (bu skript faqat tekshiradi);
  - qo'shimcha dev manzillar (gazetteer) — trip_order_db.places ga (idempotent);
  - yo'lovchi: OTP -> profil ismi;
  - haydovchi: OTP -> ro'yxatdan o'tish 1-4 (hujjatlar yuklanadi) -> tasdiq (avto-tasdiq yoki operator navbati);
  - xodimlar: superadmin (scripts/seed-superadmin.sh), keyin owner/manager/marketer/operator — POST /admin/v1/staff.

Talab: identity'da DEV_MODE=true, DEV_OTP_CODE va quyidagi telefon/email'lar DEV_TEST_PHONES / DEV_TEST_EMAILS da
(docs/dev-testing.md). Qayta ishga tushirish xavfsiz: mavjudlari o'tkazib yuboriladi.

  ./scripts/seed-dev.sh            # .env dan DEV_OTP_CODE ni o'qiydi
"""
import json
import os
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib

KONG = os.environ.get("KONG_URL", "http://localhost:8000").rstrip("/")
CODE = os.environ.get("DEV_OTP_CODE", "")
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

PASSENGER = {"phone": "+998900000001", "first_name": "Test", "last_name": "Yo'lovchi"}
DRIVER = {"phone": "+998900000002", "first_name": "Test", "last_name": "Haydovchi"}
STAFF = [  # (rol, email, telefon, ism) — rol ID lari shared/roles/roles.json dagi
    ("superadmin", "superadmin@izgo.test", "+998900000010", "Test Superadmin"),
    ("owner", "owner@izgo.test", "+998900000011", "Test Owner"),
    ("manager", "manager@izgo.test", "+998900000012", "Test Menejer"),
    ("marketer", "marketer@izgo.test", "+998900000013", "Test Marketolog"),
    ("operator", "operator@izgo.test", "+998900000014", "Test Operator"),
]
TASHKENT = (41.3111, 69.2797)

# Dev manzillar (taxminiy koordinatalar; faqat qidiruvni sinash uchun). ID lar barqaror.
DEV_PLACES = [
    ("11111111-0000-5000-8000-000000000001", "toshkent", "Amir Temur xiyoboni", "Yunusobod", "landmark", 41.3111, 69.2797),
    ("11111111-0000-5000-8000-000000000002", "toshkent", "Chorsu bozori", "Shayxontohur", "market", 41.3264, 69.2353),
    ("11111111-0000-5000-8000-000000000003", "toshkent", "Toshkent xalqaro aeroporti", "Sergeli", "airport", 41.2579, 69.2812),
    ("11111111-0000-5000-8000-000000000004", "toshkent", "Toshkent shimoliy vokzali", "Mirobod", "station", 41.2918, 69.2867),
    ("11111111-0000-5000-8000-000000000005", "samarqand", "Registon maydoni", "Markaz", "landmark", 39.6548, 66.9757),
    ("11111111-0000-5000-8000-000000000006", "samarqand", "Samarqand vokzali", "Temir yo'l", "station", 39.6800, 66.9380),
    ("11111111-0000-5000-8000-000000000007", "buxoro", "Labi Hovuz", "Markaz", "landmark", 39.7739, 64.4196),
]


def die(msg):
    print(f"  XATO  {msg}", file=sys.stderr)
    sys.exit(1)


def ok(msg):
    print(f"  OK    {msg}")


def call(method, path, body=None, token=None, headers=None, raw=None, url=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(url or KONG + path, data=data, method=method)
    if raw is None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            txt = r.read().decode()
            return r.status, (json.loads(txt) if txt else None)
    except urllib.error.HTTPError as e:
        txt = e.read().decode()
        try:
            return e.code, json.loads(txt)
        except ValueError:
            return e.code, txt


def expect(status, want, what, body):
    if status not in (want if isinstance(want, tuple) else (want,)):
        die(f"{what}: HTTP {status} {body}")


def png_bytes():
    """Haqiqiy (kichik) PNG: driver servisi magic bytes va chunk tuzilishini tekshiradi."""
    def chunk(t, d):
        return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
    w = h = 32
    rows = b"".join(b"\x00" + bytes([200, 120, 40]) * w for _ in range(h))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b""))


def otp_request(path, body, what):
    """Qayta yuborish oralig'i (resend_after) ichida bo'lsa kutib, bir marta qayta uriniladi."""
    s, b = call("POST", path, body)
    if s == 429:
        wait = int(((b or {}).get("error", {}).get("params", {}) or {}).get("retry_after", 0))
        if 0 < wait <= 70:
            print(f"        {what}: qayta so'rash uchun {wait} s kutilmoqda")
            time.sleep(wait + 1)
            s, b = call("POST", path, body)
        else:
            die(f"{what}: soatlik OTP limiti tugadi (telefon boshiga 5/soat) — keyinroq urinib ko'ring")
    expect(s, 202, what, b)
    return b


def user_login(phone, app):
    otp_request("/api/v1/auth/otp/request", {"phone": phone}, f"otp/request {phone}")
    s, b = call("POST", "/api/v1/auth/otp/verify", {"phone": phone, "code": CODE}, headers={"X-App": app})
    if s == 401:
        die(f"otp/verify {phone}: kod mos kelmadi — identity'da DEV_MODE=true va telefon DEV_TEST_PHONES da ekanini tekshiring")
    expect(s, 200, f"otp/verify {phone}", b)
    return b["access_token"]


def admin_login(email, phone):
    b = otp_request("/admin/v1/auth/login/email", {"email": email}, f"admin login/email {email}")
    b = otp_request("/admin/v1/auth/login/sms", {"challenge_id": b["challenge_id"], "email_code": CODE, "phone": phone},
                    f"admin login/sms {email}")
    s, b = call("POST", "/admin/v1/auth/login/verify", {"challenge_id": b["challenge_id"], "sms_code": CODE})
    if s == 401:
        die(f"admin login/verify {email}: kod mos kelmadi — DEV_TEST_EMAILS / DEV_TEST_PHONES ni tekshiring")
    expect(s, 200, f"admin login/verify {email}", b)
    return b["access_token"]


def docker_psql(db, sql, variables):
    user = os.environ.get("POSTGRES_USER", "")
    if not user:
        die("POSTGRES_USER kerak (.env)")
    cmd = ["docker", "compose", "exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1", "-qAt", "-U", user, "-d", db]
    for k, v in variables.items():
        cmd += ["-v", f"{k}={v}"]
    r = subprocess.run(cmd, input=sql, text=True, capture_output=True, cwd=ROOT)
    if r.returncode != 0:
        die(f"psql {db}: {r.stderr.strip()}")
    return r.stdout.strip()


def seed_reference():
    print("1) Ma'lumotnomalar (migratsiyalar) va dev manzillar")
    s, b = call("GET", "/api/v1/cities")
    expect(s, 200, "cities", b)
    if len(b["data"]) < 20:
        die(f"shaharlar kam ({len(b['data'])}): trip-order migratsiyasi ishlamagan")
    ok(f"shaharlar: {len(b['data'])}")
    s, b = call("GET", "/api/v1/routes/popular")
    expect(s, 200, "routes/popular", b)
    priced = sum(1 for r in b["data"] if r["price_from"])
    ok(f"mashhur yo'nalishlar: {len(b['data'])} (narxi bor: {priced} — tarif ishlayapti)")
    values = ",\n".join(
        f"('{pid}', (SELECT id FROM cities WHERE slug='{slug}'), $${name}$$, $${area}$$, '{kind}', {lat}, {lng}, "
        f"lower(translate($${name}$$, $$'`‘’ʻʼ$$, '')))"
        for pid, slug, name, area, kind, lat, lng in DEV_PLACES)
    docker_psql("trip_order_db", f"""INSERT INTO places (id, city_id, name, area, kind, lat, lng, search_key) VALUES
{values}
ON CONFLICT (id) DO NOTHING;""", {})
    ok(f"dev manzillar: {len(DEV_PLACES)} ta (trip_order_db.places)")


def seed_passenger():
    print("2) Yo'lovchi")
    tok = user_login(PASSENGER["phone"], "client")
    s, b = call("PUT", "/api/v1/profile", {"first_name": PASSENGER["first_name"], "last_name": PASSENGER["last_name"]}, token=tok)
    expect(s, 200, "profile", b)
    ok(f"{PASSENGER['phone']} — {b.get('first_name')} {b.get('last_name')}")


def upload_doc(tok, kind):
    img = png_bytes()
    s, b = call("POST", "/api/v1/drivers/documents/upload-url", {"kind": kind, "content_type": "image/png", "size_bytes": len(img)}, token=tok)
    expect(s, 201, f"upload-url {kind}", b)
    # imzolangan URL tokensiz PUT qilinadi; host PUBLIC_BASE_URL dan, yo'lini Kong orqali yuboramiz
    u = urllib.parse.urlsplit(b["upload_url"])
    target = KONG + u.path + ("?" + u.query if u.query else "")
    hdrs = dict(b.get("headers") or {})
    hdrs.setdefault("Content-Type", "image/png")
    s2, b2 = call("PUT", "", raw=img, headers=hdrs, url=target)
    expect(s2, 204, f"upload {kind}", b2)
    return b["document_id"]


def seed_driver(operator_token_fn):
    print("4) Haydovchi (tasdiqlangan)")
    tok = user_login(DRIVER["phone"], "driver")
    s, st = call("GET", "/api/v1/drivers/me/status", token=tok)
    expect(s, 200, "driver status", st)
    state = st.get("state")
    if state in ("draft", "rejected"):
        steps = [
            (1, {"first_name": DRIVER["first_name"], "last_name": DRIVER["last_name"], "birth_date": "1990-01-01", "city": "Toshkent"}),
            (2, {"car": {"brand": "Chevrolet", "model": "Cobalt", "year": 2022, "color": "white", "plate_number": "01A001AA",
                         "car_class": "economy", "seats": 4}}),
        ]
        for n, body in steps:
            s, b = call("POST", f"/api/v1/drivers/registration/step/{n}", body, token=tok)
            expect(s, 200, f"registration step {n}", b)
        docs = [{"kind": k, "document_id": upload_doc(tok, k)} for k in ("license_front", "tech_passport_front", "selfie")]
        for n, body in ((3, {"documents": docs}), (4, {"accept_terms": True})):
            s, b = call("POST", f"/api/v1/drivers/registration/step/{n}", body, token=tok)
            expect(s, 200, f"registration step {n}", b)
        ok("ro'yxatdan o'tish yuborildi")
    for _ in range(20):  # avto-tasdiq (AUTO_APPROVE_ENABLED=true, 5 s)
        s, st = call("GET", "/api/v1/drivers/me/status", token=tok)
        if st.get("state") == "active":
            break
        time.sleep(1)
    if st.get("state") == "pending_review":
        op = operator_token_fn()
        s, q = call("GET", "/admin/v1/operator/queue?limit=100", token=op)
        expect(s, 200, "operator queue", q)
        items = [i for i in q["data"] if i["kind"] == "driver_application" and i["status"] in ("waiting", "in_progress")]
        for i in items:
            if i["status"] == "waiting":
                call("POST", f"/admin/v1/operator/queue/{i['id']}/start", token=op)
            call("POST", f"/admin/v1/operator/queue/{i['id']}/complete", {"result": "resolved", "comment": "dev seed"}, token=op)
        for _ in range(20):
            s, st = call("GET", "/api/v1/drivers/me/status", token=tok)
            if st.get("state") == "active":
                break
            time.sleep(1)
    if st.get("state") != "active":
        die(f"haydovchi tasdiqlanmadi: {st}")
    ok(f"{DRIVER['phone']} — active")
    s, b = call("POST", "/api/v1/location/update", {"lat": TASHKENT[0], "lng": TASHKENT[1]}, token=tok)
    expect(s, 204, "location/update", b)
    ok("GPS: Toshkent markazi (LOCATION_TTL ichida takliflar shu haydovchiga ham boradi)")


def seed_staff():
    print("3) Xodimlar (har rolga bittadan)")
    role, email, phone, name = STAFF[0]
    env = dict(os.environ, ADMIN_BOOTSTRAP_EMAIL=email, ADMIN_BOOTSTRAP_PHONE=phone, ADMIN_BOOTSTRAP_NAME=name)
    r = subprocess.run([os.path.join(ROOT, "scripts", "seed-superadmin.sh")], env=env, capture_output=True, text=True, cwd=ROOT)
    if r.returncode != 0:
        die(f"seed-superadmin.sh: {r.stderr.strip() or r.stdout.strip()}")
    sa = admin_login(email, phone)
    ok(f"superadmin  {email} {phone}")
    s, b = call("GET", "/admin/v1/staff?limit=100", token=sa)
    expect(s, 200, "staff list", b)
    have = {x["phone"] for x in b["data"]}
    for role, email, phone, name in STAFF[1:]:
        if phone not in have:
            s, b = call("POST", "/admin/v1/staff", {"email": email, "phone": phone, "full_name": name, "role": role}, token=sa)
            expect(s, (200, 201), f"staff create {role}", b)
        ok(f"{role:<11} {email} {phone}")
    return lambda: admin_login(STAFF[4][1], STAFF[4][2])


def main():
    host = urllib.parse.urlsplit(KONG).hostname
    if host not in ("localhost", "127.0.0.1") and os.environ.get("SEED_ALLOW_REMOTE") != "1":
        die(f"KONG_URL={KONG}: dev seed faqat lokal stek uchun (boshqa host uchun SEED_ALLOW_REMOTE=1)")
    if not (CODE.isdigit() and len(CODE) == 6):
        die("DEV_OTP_CODE (6 raqam) kerak — .env da DEV_MODE=true bilan birga (docs/dev-testing.md)")
    seed_reference()
    seed_passenger()
    operator = seed_staff()
    seed_driver(operator)
    print("Tayyor. Test hisoblar va kodlar: docs/dev-testing.md")


if __name__ == "__main__":
    main()
