---
title: Callback Contract
description: Signed final-state callbacks from Paystable to your backend.
---

Paystable sends final outcomes to the hold `callback_url`. Treat this callback as the trusted trigger for fulfillment or release.

## Request

```http
POST <callback_url>
Content-Type: application/json
X-Paystable-Signature: v2=<hex-hmac>
X-Paystable-Idempotency-Key: <opaque-key>
X-Paystable-Timestamp: <unix-seconds>
```

## Payload

```json
{
  "txn_id": "order_abc123",
  "event": "transaction.confirmed",
  "status": "CONFIRMED",
  "amount": 49900,
  "currency": "INR",
  "gateway": "payu",
  "verified_at": "2026-06-24T12:00:19Z",
  "metadata": {
    "order_id": "order_abc123"
  }
}
```

Review states can include `reason`, `gateway_amount`, and `hold_amount`.

| Field | Notes |
|---|---|
| `event` | `transaction.confirmed`, `transaction.failed`, `transaction.indeterminate`, or `transaction.mismatch`. |
| `status` | `CONFIRMED`, `FAILED`, `INDETERMINATE`, or `MISMATCH`. |
| `amount` | Hold amount, in smallest currency unit. |
| `metadata` | Original hold metadata. |

## Verify Signature

The `v2=` signature authenticates the delivery timestamp, event key, and raw body with HMAC-SHA256 using `MERCHANT_CALLBACK_SECRET`.
Sign the UTF-8 bytes `v2\n<timestamp>\n<idempotency-key>\n` followed by the unchanged raw body. Each `\n` is one LF byte.
Reject timestamps more than five minutes in the past or future. Retries keep the same key and body and receive a fresh signed timestamp.

**Breaking change:** Update your merchant verifier when upgrading Paystable. Reject legacy body-only `sha256=` signatures; do not accept them as a fallback.

```js
import crypto from "node:crypto";

export function verifyPaystableCallback(rawBody, signature, key, timestamp, secret) {
  if (!secret || !key || /[\r\n]/.test(key) ||
      !/^(0|[1-9]\d*)$/.test(timestamp ?? "") ||
      !/^v2=[0-9a-f]{64}$/.test(signature ?? "")) return false;

  const seconds = Number(timestamp);
  const now = Math.floor(Date.now() / 1000);
  if (!Number.isSafeInteger(seconds) || seconds < now - 300 || seconds > now + 300) {
    return false;
  }

  const received = Buffer.from(signature.slice("v2=".length), "hex");
  const expected = crypto
    .createHmac("sha256", secret)
    .update(`v2\n${timestamp}\n${key}\n`)
    .update(rawBody)
    .digest();

  return received.length === expected.length &&
    crypto.timingSafeEqual(received, expected);
}
```

Pass the unchanged raw body and the signature, idempotency key, and timestamp headers. Verify before JSON parsing or trusting the key.

## Idempotency

Paystable delivers at least once. After signature verification, store `X-Paystable-Idempotency-Key` and apply fulfillment in the same database transaction. If the same key arrives again, return `2xx` and skip processing. Also enforce one fulfillment per `txn_id` with a database constraint.

Treat the key as opaque.

## Retry Behavior

| Merchant response | Paystable action |
|---|---|
| `2xx` | Mark delivered. |
| `4xx` except `429` | Mark exhausted. |
| `429`, `5xx`, timeout | Retry with backoff. |

Default timeout: `DELIVERY_TIMEOUT_S=10`.

Production callback URLs must be HTTPS unless `DELIVERY_ALLOW_INSECURE_CALLBACK=true` is set for local development.
