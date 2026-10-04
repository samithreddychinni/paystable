# testkit

local end-to-end test environment. runs paystable + a mock PayU gateway + a mock merchant app, all in docker.

## setup

fill in your PayU test credentials:

```bash
cp .env.testkit.example .env.testkit
# edit .env.testkit with your test key and salt
```

## start the environment

```bash
docker compose -f docker-compose.testkit.yml --env-file .env.testkit up --build
```

paystable:    http://localhost:8080  
mock gateway: http://localhost:9090  
mock merchant: http://localhost:9091  

The scenarios send callbacks to `http://merchant:9091/callback` inside the stack.
Set `MERCHANT_CALLBACK_URL` to use a different callback endpoint.

## run a scenario

```bash
go run ./testkit/scenarios <scenario>
```

available scenarios:

| scenario | what it tests |
|---|---|
| `happy-path` | webhook says success, gateway confirms success. hold → CONFIRMED. |
| `false-failure` | webhook says FAILED, but gateway actually returns success after 25s. paystable should still reach CONFIRMED. this is the core case paystable exists to solve. |
| `genuine-failure` | webhook and gateway both say failed. hold → FAILED. |
| `amount-mismatch` | gateway reports success but wrong amount. hold -> MISMATCH. |
| `merchant-offline` | merchant callback endpoint is down. hold confirms, delivery retries. bring merchant back online to see delivery succeed. |
| `duplicate-webhook` | same webhook fired 3 times. should not produce duplicate state transitions. |

## how the mock gateway works

**script a transaction:**  
`POST http://localhost:9090/script`  
body: `{ "txn_id": "x", "status": "success", "amount": 49900, "fail_until_s": 30 }`  
setting `fail_until_s` makes the status endpoint return "failed" for that many seconds, then switch to the scripted status. this simulates replica lag.

**fire a webhook:**  
`POST http://localhost:9090/fire-webhook`  
body: `{ "txn_id": "x", "status": "failure" }`  
sends a correctly HMAC-signed PayU webhook to paystable.

## take merchant offline

```bash
curl -X POST http://localhost:9091/toggle-offline
```

call again to bring it back online.

## Razorpay

Use the same stack for Razorpay tests:

```bash
GATEWAY=razorpay docker compose -f docker-compose.testkit.yml --env-file .env.testkit up --build
GATEWAY=razorpay go run ./testkit/scenarios razorpay-success
GATEWAY=razorpay go run ./testkit/scenarios razorpay-failed-webhook
```

The mock API uses test credentials and integer paise amounts.
The second scenario sends a failed webhook while the status API returns a captured payment.
Both scenarios require `CONFIRMED`.
