# Hosting the backend for the hosted frontends

The three frontends (client, driver, admin) are static sites on free HTTPS hosting (see `onlyfront/DEPLOY.md`).
An `https://` page can only call an `https://` API and a `wss://` WebSocket, and the browser only lets it do so
when CORS allows its origin.

## 1. HTTPS and WSS (Caddy in front of Kong)

The `tls` compose profile adds Caddy on ports 80 and 443. It gets a Let's Encrypt certificate for `API_DOMAIN`,
redirects HTTP to HTTPS and proxies everything, including the `/ws` WebSocket upgrade, to Kong.

1. Point a DNS A/AAAA record (for example `api.izgo.uz`) at the server and open ports 80 and 443.
2. In `.env`, set:
   ```dotenv
   API_DOMAIN=api.izgo.uz
   PUBLIC_BASE_URL=https://api.izgo.uz        # driver document upload URLs are signed with this origin
   APP_ENV=production
   ```
3. Start the stack with the profile: `docker compose --profile tls up -d`.
4. Check: `curl -I https://api.izgo.uz/api/v1/cities` returns `200` with a valid certificate.
   `wss://api.izgo.uz/ws` is then the WebSocket URL.
5. Close Kong's host ports (`8000`, `8001`) to the internet with a firewall, or remove the `ports:` of `kong` in
   `docker-compose.yml`. Only Caddy should be public. Never expose the Kong admin port `8001`.

`gateway/Caddyfile` also scrubs `?token=`, `Authorization` and `Sec-WebSocket-Protocol` from Caddy's access log.

**Not verified in this repo:** certificate issuance needs a real domain and open ports. The Caddyfile is validated
with `caddy validate`, and the compose profile with `docker compose config`.

If you use another TLS terminator (a cloud load balancer, Cloudflare), forward HTTPS and WebSocket upgrades to Kong
`:8000` and skip the profile.

## 2. CORS (Kong)

`gateway/kong.yml` has a global `cors` plugin:

- **Origins:** the 3 frontend domains, still placeholders (`https://CLIENT_DOMAIN.example`, ...), plus localhost Vite
  ports for development. Replace the placeholders with the real domains, for example
  `https://izgo-client.vercel.app`. Use exact origins (scheme + host, no path, no trailing slash) and no wildcard.
- **Methods:** `GET, POST, PUT, PATCH, DELETE, OPTIONS`.
- **Request headers:** `Authorization, Content-Type, X-App, Idempotency-Key`.
- **Exposed headers:** `X-Request-ID, Retry-After`.
- `credentials: false` (the frontends send no cookies) and `max_age: 600`.

After editing `kong.yml`: `docker compose restart kong`.

## 3. WebSocket origins (notification)

CORS does not apply to WebSocket upgrades. The notification service checks `Origin` itself, so put the same 3 hosts
(host only, no scheme) in `.env`:

```dotenv
WS_ALLOWED_ORIGINS=izgo-client.vercel.app,izgo-driver.vercel.app,izgo-admin.vercel.app
```

Then `docker compose up -d notification`. Native apps send no `Origin` and are not affected.

Once all frontends use the subprotocol auth (`docs/websocket-events.md`), set `WS_ALLOW_QUERY_TOKEN=false`.

## 4. Frontend side

On each of the 3 sites, set `VITE_API_URL=https://api.izgo.uz` and redeploy. The frontend build adds the API origin
and its `wss://` form to the CSP `connect-src` itself.

## Checklist

- [ ] DNS for `API_DOMAIN`; ports 80/443 open; Kong ports closed to the internet
- [ ] `.env`: `API_DOMAIN`, `PUBLIC_BASE_URL=https://…`, `APP_ENV=production`, `DEV_MODE` empty/false,
      `AUTO_APPROVE_ENABLED=false`, `MOCK_LOG_SMS_PARAMS=false`
- [ ] `kong.yml` CORS origins: the 3 real frontend domains; placeholders removed
- [ ] `WS_ALLOWED_ORIGINS`: the same 3 hosts
- [ ] `docker compose --profile tls up -d`; `curl -I https://<API_DOMAIN>/api/v1/cities` → 200
- [ ] Preflight check: `curl -i -X OPTIONS https://<API_DOMAIN>/api/v1/orders -H 'Origin: https://<client domain>' -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: authorization,content-type,idempotency-key'`
      returns `Access-Control-Allow-Origin: https://<client domain>`
