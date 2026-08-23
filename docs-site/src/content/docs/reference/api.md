---
title: API Reference
description: Current Paystable HTTP endpoints.
---

## Authentication

Hold creation and backend status reads use:

```http
Authorization: Bearer <ADMIN_API_KEY>
```

Frontend reads use the per-hold `read_token` returned by `POST /api/v1/hold`.

## Create Hold

```http
POST /api/v1/hold
Authorization: Bearer <ADMIN_API_KEY>
Content-Type: application/json
```

```json
{
  "txn_id": "order_abc123",
  "gateway": "payu",
  "amount": 49900,
  "currency": "INR",
  "ttl_seconds": 300,
  "callback_url": "https://merchant.example/paystable/callback",
  "metadata": {
    "order_id": "order_abc123"
  }
}
```

Required fields: `txn_id`, `gateway`, `amount`, `callback_url`.

Response:

```json
{
  "txn_id": "order_abc123",
  "status": "PENDING",
  "read_token": "pst_rt_...",
  "expires_at": "2026-06-24T12:05:00Z",
  "created_at": "2026-06-24T12:00:00Z"
}
```

## Get Status

Frontend:

```http
GET /api/v1/transactions/{txn_id}/status?token={read_token}
```

Backend:

```http
GET /api/v1/transactions/{txn_id}/status
Authorization: Bearer <ADMIN_API_KEY>
```

Response:

```json
{
  "txn_id": "order_abc123",
  "status": "VERIFYING",
  "gateway": "payu",
  "amount": 49900,
  "currency": "INR",
  "expires_at": "2026-06-24T12:05:00Z",
  "created_at": "2026-06-24T12:00:00Z",
  "updated_at": "2026-06-24T12:00:12Z"
}
```

## Stream Status

```http
GET /api/v1/transactions/{txn_id}/stream?token={read_token}
Accept: text/event-stream
```

Event:

```text
event: status_change
data: {"status":"CONFIRMED","at":"2026-06-24T12:00:19Z"}
```

The stream closes after `CONFIRMED`, `FAILED`, `REFUNDED`, `INDETERMINATE`, or `MISMATCH`.

## Timeline

```http
GET /api/v1/transactions/{txn_id}/timeline?token={read_token}
```

Backend callers may omit `token` when using `Authorization: Bearer <ADMIN_API_KEY>`.

## Gateway Webhook

```http
POST /webhooks/{gateway}
```

Supported adapters: `payu` and `razorpay`.

Paystable validates the gateway signature before writing to `webhooks`. Invalid requests are quarantined in `webhooks_rejected`.

## Localhost Admin APIs

Dashboard APIs are loopback-only:

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/admin/overview/stats` | Dashboard summary. |
| `GET /api/v1/admin/transactions` | Transaction list. |
| `GET /api/v1/admin/transactions/{id}` | Transaction detail. |
| `GET /api/v1/admin/mismatches` | Webhook-vs-verified contradictions. |
| `GET /api/v1/admin/deliveries` | Delivery list. |
| `POST /api/v1/admin/deliveries/{id}/replay` | Replay exhausted delivery. |
| `GET /api/v1/admin/config` | Config visibility. |
| `POST /api/v1/admin/config` | Update local `.env` values. |
| `POST /api/v1/admin/config/rotate-secret` | Rotate gateway webhook secret. |
| `GET /api/v1/admin/export/ledger` | Export ledger as JSON/CSV. |
