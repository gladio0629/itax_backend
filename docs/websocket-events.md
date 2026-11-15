# WebSocket events (`/ws`)

The notification service serves `GET /ws` (through Kong: `wss://<api-domain>/ws`). It pushes small JSON
text frames that tell an app **what changed**. The app then re-reads the object over REST (`GET /api/v1/orders/{id}`,
`GET /api/v1/trips/offers`, `GET /api/v1/wallet/balance`, ...). Frames never carry names, phone numbers,
addresses or prices.

The contract for the handshake is `notificationsWebSocket` in `api/openapi.yml`. This file lists the events.

## Connecting

Preferred: the token in a subprotocol, so it never appears in a URL or an access log.

```js
const ws = new WebSocket("wss://api.example.uz/ws", ["izgo.v1", "bearer." + accessToken]);
// server answers with Sec-WebSocket-Protocol: izgo.v1 (never echoes the bearer entry)
```

Alternative: open without a token and send one auth frame within `WS_AUTH_TIMEOUT` (default 5 s).

```js
const ws = new WebSocket("wss://api.example.uz/ws", ["izgo.v1"]);
ws.onopen = () => ws.send(JSON.stringify({ type: "auth", token: accessToken }));
// server replies {"type":"auth.ok"}, then sends events
```

`/ws?token=…` still works while `WS_ALLOW_QUERY_TOKEN=true` (the default until the frontends switch), but it is
deprecated: the token lands in the Kong access log.

After authentication the client must not send data frames (the server closes with `1008`).

## Frame format

Every frame is one JSON object, and every value is a string:

```json
{ "type": "order.accepted", "id": "0193…", "status": "accepted", "order_id": "0193…" }
```

| Field | Always | Meaning |
|---|---|---|
| `type` | yes | Event name (table below) |
| `id` | yes | UUID of the event's main object (see "id is" column) |
| `status` | only for order status changes | The order's new status (`OrderStatus` in the contract) |
| `order_id` | only when `status` is present | The order whose status changed |

Unknown `type` values must be ignored (new events may be added).

## Control frames

| Frame | When |
|---|---|
| `{"type":"auth.ok"}` | First-message auth succeeded |

## Events

| `type` | Sent to | When | `id` is | `status` | Payload |
|---|---|---|---|---|---|
| `order.created` | passenger | Order created (`POST /api/v1/orders`) | order id | `searching` | `{"type":"order.created","id":"<order_id>","status":"searching","order_id":"<order_id>"}` |
| `offer.created` | driver | A new offer for this driver (new order, or an order back in search after another driver cancelled) | offer id | — | `{"type":"offer.created","id":"<offer_id>"}` |
| `order.accepted` | passenger | A driver accepted the order | order id | `accepted` | `{"type":"order.accepted","id":"<order_id>","status":"accepted","order_id":"<order_id>"}` |
| `order.arrived` | passenger | Driver pressed "arrived" (`POST /api/v1/trips/{id}/arrive`) | order id | `arrived` | `{"type":"order.arrived","id":"<order_id>","status":"arrived","order_id":"<order_id>"}` |
| `trip.started` | passenger | Trip started (`POST /api/v1/trips/{id}/start`) | trip id | `in_progress` | `{"type":"trip.started","id":"<trip_id>","status":"in_progress","order_id":"<order_id>"}` |
| `trip.completed` | passenger | Trip completed (`POST /api/v1/trips/{id}/complete`) | trip id | `completed` | `{"type":"trip.completed","id":"<trip_id>","status":"completed","order_id":"<order_id>"}` |
| `trip.cancelled` | passenger | The **driver** cancelled an unstarted trip; the order is searching again | trip id | `searching` | `{"type":"trip.cancelled","id":"<trip_id>","status":"searching","order_id":"<order_id>"}` |
| `trip.cancelled` | driver | An **admin** cancelled the order this driver was assigned to | trip id | `cancelled` | `{"type":"trip.cancelled","id":"<trip_id>","status":"cancelled","order_id":"<order_id>"}` |
| `order.cancelled` | passenger | An **admin** cancelled the order | order id | `cancelled` | `{"type":"order.cancelled","id":"<order_id>","status":"cancelled","order_id":"<order_id>"}` |
| `wallet.limit_reached` | driver | Balance crossed the debt limit (no new orders until topped up) | wallet id | — | `{"type":"wallet.limit_reached","id":"<wallet_id>"}` |
| `driver.approved` | driver | Registration approved | driver (user) id | — | `{"type":"driver.approved","id":"<driver_id>"}` |

Not sent over WebSocket: a passenger's own cancellation (the app already has the response), order expiry
(`expired`; the order page shows it on the next read), and offer expiry.

### What the app should do

| App | Event | Action |
|---|---|---|
| Client | any frame with `order_id` | Re-read `GET /api/v1/orders/{order_id}` |
| Driver | `offer.created` | Re-read `GET /api/v1/trips/offers` |
| Driver | `trip.cancelled` | Re-read `GET /api/v1/trips` (the trip is gone from the active list) |
| Driver | `wallet.limit_reached` | Re-read `GET /api/v1/wallet/balance` |
| Driver | `driver.approved` | Re-read `GET /api/v1/drivers/me/status` |

## Close codes

| Code | Reason | Client action |
|---|---|---|
| `4401` | `token_expired` | Refresh the token, reconnect |
| `4401` | `session_revoked` | Log out |
| `4401` | `token_invalid` / `auth_timeout` | First-message auth failed: refresh, reconnect |
| `4403` | `ws_connection_limit` | Too many open tabs (`WS_MAX_CONNS`, default 3) |
| `1008` | `auth_malformed` or a data frame after auth | Client bug |
| `1001` | `server_shutdown` | Reconnect with backoff |
| `1011` | `internal_error` | Reconnect with backoff |

Before the upgrade (subprotocol or legacy query token): `401` for a bad token, `403` for the connection limit or a
disallowed `Origin` (`WS_ALLOWED_ORIGINS`).

## Where events come from

The notification service listens to NATS JetStream (`shared/events/*.json` has the full payload schemas) and
maps each event to one recipient:

| NATS subject | Producer | WS recipient |
|---|---|---|
| `izgo.order.created.v1` | trip-order | `payload.passenger_id` |
| `izgo.offer.created.v1` | matching-geo | `payload.driver_id` |
| `izgo.order.accepted.v1` | trip-order | passenger of `payload.order_id` |
| `izgo.order.arrived.v1` | trip-order | `payload.passenger_id` |
| `izgo.trip.started.v1` | trip-order | `payload.passenger_id` |
| `izgo.trip.completed.v1` | trip-order | `payload.passenger_id` |
| `izgo.trip.cancelled.v1` | trip-order | passenger if `cancelled_by = driver`, driver if `cancelled_by = admin` |
| `izgo.order.cancelled.v1` | trip-order | `payload.passenger_id` |
| `izgo.wallet.limit_reached.v1` | wallet | `payload.driver_id` |
| `izgo.driver.approved.v1` | driver / backoffice | `payload.driver_id` |

If the recipient has no open connection, the same `{type, id}` goes out as a push notification (mock today).
