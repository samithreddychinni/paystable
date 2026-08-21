# Razorpay Test Mode flow

This service creates one Razorpay order and the matching Paystable hold.
It verifies Checkout success on the server.
It fulfills only after a signed Paystable callback.

The webhook relay saves the signed Razorpay event before it forwards the event to Paystable.

## Requirements

- Docker with Compose
- Razorpay Payment Gateway Test Mode keys
- a Razorpay webhook secret
- a public HTTPS URL for port `9092`

Razorpay does not send webhooks to `localhost`.
Its documentation recommends zrok for local webhook testing.

## Run the flow

1. Set the following values in `.env`:

   - `RAZORPAY_KEY_ID`
   - `RAZORPAY_KEY_SECRET`
   - `RAZORPAY_WEBHOOK_SECRET`
   - `MERCHANT_CALLBACK_SECRET`
   - `ADMIN_API_KEY`

2. Start the services:

   ```bash
   docker compose -f docker-compose.razorpay.yml up --build
   ```

3. Expose `http://localhost:9092` through a public HTTPS tunnel.

4. Open the Razorpay Dashboard in Test Mode.

5. Enable automatic payment capture.

6. Set the webhook URL to this value:

   ```text
   https://YOUR-PUBLIC-HOST/webhooks/razorpay
   ```

7. Use the same webhook secret that is in `.env`.

8. Enable the `payment.captured` webhook event.

9. Open `http://localhost:9092`.

10. Select **Create test payment**.

11. Complete Checkout with Razorpay test payment data.

12. Check the accepted fulfillment effect:

    ```bash
    curl http://localhost:9092/effects
    ```

13. Check the saved signed fixture:

    ```text
    artifacts/razorpay-webhook.json
    ```

The Checkout result does not fulfill the order.
Paystable waits for the signed webhook and confirms the payment through the Razorpay API.
