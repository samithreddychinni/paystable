---
title: Razorpay
description: Verify Razorpay orders before fulfillment.
---

Razorpay support requires a source build from `main`.
The v0.3.0 release binaries support PayU only.

## Configure

**Warning:** Enable auto-capture in the Razorpay dashboard.
Paystable does not capture payments.
An authorized payment is not a captured payment.

Set these values:

```dotenv
GATEWAY=razorpay
RAZORPAY_KEY_ID=<test-key-id>
RAZORPAY_KEY_SECRET=<test-key-secret>
WEBHOOK_SECRET=<webhook-secret>
```

Use gateway test mode for the first payment.
Keep the keys and webhook secret private.
The API URL defaults to `https://api.razorpay.com/v1`.
The mock testkit uses `RAZORPAY_API_URL` to select its local API.
PayU credentials are not required when `GATEWAY=razorpay`.

Run `./paystable doctor` before you start the service.
The command checks configuration and prints the auto-capture requirement.
It does not prove that the gateway accepts the keys.

## Create a Hold

1. Create a Razorpay order with the full amount in paise and currency `INR`.
2. Set the order receipt to your merchant order ID.
3. Create a Paystable hold with the Razorpay `order_id` as `txn_id`.
4. Set the hold gateway to `razorpay`.
5. Keep the merchant order ID in the hold metadata.
6. Start checkout after the hold exists.

Paystable polls the order and its payment attempts with Basic auth.
A `payment_id` identifies one attempt.
The receipt does not select the status poll.
A captured attempt takes precedence over earlier failed attempts.

## Webhooks

Use `POST /webhooks/razorpay`.
Subscribe to these events:

- `payment.captured`
- `payment.failed`
- `payment.authorized`
- `order.paid`

Paystable checks HMAC-SHA256 over the raw body before JSON decode.
It compares the signature in constant time.
It accepts active secrets during the configured rotation window.
It deduplicates events with `X-Razorpay-Event-Id` or a raw-body digest.
It stores other event types without scheduling a status poll.
A webhook that arrives before its hold can trigger verification after hold creation.

## Final States

A failed attempt stays pending before the hold expires.
Normal confirmation requires consecutive matching API results.
At expiry, Paystable performs the same final API check as the PayU path.

| API result | State |
|---|---|
| Captured attempt and paid order, with the expected amount | `CONFIRMED` |
| Different amount or partial payment | `MISMATCH` |
| Any created or authorized attempt without capture | `INDETERMINATE` |
| All attempts failed, or no attempts | `FAILED` |
| Refund before confirmation | `INDETERMINATE` |
| Timeout, server error, or invalid response at expiry | `INDETERMINATE` |

A timeout or server error before expiry schedules another check.
It does not make the hold terminal.
A captured payment after `FAILED` creates a ledger event and an alert.
The hold stays `FAILED`.
Review the payment manually before any fulfillment or refund.

## References

- [Webhook signatures](https://razorpay.com/docs/webhooks/validate-test/)
- [Payment events](https://razorpay.com/docs/webhooks/payments/)
- [Payment API](https://razorpay.com/docs/api/payments/fetch-with-id/)
- [Order API](https://razorpay.com/docs/api/orders/fetch-with-id/)
- [Order payment attempts](https://razorpay.com/docs/api/orders/fetch-payments/)
- [Capture settings](https://razorpay.com/docs/payments/payments/capture-settings/)
