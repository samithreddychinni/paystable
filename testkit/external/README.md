# External implementation check

This check executes an independently authored Razorpay webhook handler.
It does not copy the handler into this repository.

## Binding source

- Repository: [Flexprice](https://github.com/flexprice/flexprice/tree/e4b5303fde24581003faf0b05c08feb605c2ef93)
- Commit: `e4b5303fde24581003faf0b05c08feb605c2ef93`
- License: [AGPL-3.0](https://github.com/flexprice/flexprice/blob/e4b5303fde24581003faf0b05c08feb605c2ef93/LICENSE)
- Source: [Razorpay webhook handler](https://github.com/flexprice/flexprice/blob/e4b5303fde24581003faf0b05c08feb605c2ef93/internal/integration/razorpay/webhook/handler.go)

## Run the check

The command downloads the pinned source and its Go dependencies.

```bash
./scripts/external-flexprice-check.sh
```

The probe uses the upstream test fakes and the actual webhook handler.
The probe starts after webhook signature verification.
It does not run HTTP routing or an external database.
The probe checks matched and changed payment bindings.
It changes the amount, currency, and Razorpay order ID.
The pinned handler changes all four payment cases to succeeded.

This result reproduces `INV-AMOUNT-1`, `INV-CURRENCY-1`, and `INV-ORDER-1`.
It does not measure Scout against a population of external merchants.

Run the implementation-held-out transfer report after the external check:

```bash
go run ./testkit/lab external-transfer
```

Scout trains before it loads the six external case profiles.
The transfer report disables fixed risk priors.
It reports tie-aware best and worst ranks for three matched pairs.
The command does not execute the external source again.
Scout already knows the three mismatch features from its training corpus.
This report tests implementation transfer, not a new failure family.

## HTTP and signature source

- Repository: [wpmgr](https://github.com/mosamlife/wpmgr/tree/e2fd78e9829a112ac229b1586e66ba3fd39aeaf7)
- Commit: `e2fd78e9829a112ac229b1586e66ba3fd39aeaf7`
- License: [AGPL-3.0](https://github.com/mosamlife/wpmgr/blob/e2fd78e9829a112ac229b1586e66ba3fd39aeaf7/LICENSE)
- Source: [Razorpay webhook verifier](https://github.com/mosamlife/wpmgr/blob/e2fd78e9829a112ac229b1586e66ba3fd39aeaf7/apps/api/internal/billing/razorpay/webhook.go) and [HTTP handler](https://github.com/mosamlife/wpmgr/blob/e2fd78e9829a112ac229b1586e66ba3fd39aeaf7/apps/api/internal/billing/webhook_handler.go)

Run the signature check:

```bash
./scripts/external-wpmgr-check.sh
```

The probe executes the actual verifier with the upstream test helpers.
A correctly signed Unicode body passes.
The original signature rejects a changed UTF-8 body.
The verifier also rejects a signed body without an event ID.
This result is a correct external control, not a failure finding.
The second check uses the actual HTTP route and a migrated PostgreSQL 16 container.
It revokes the ledger INSERT permission to inject a real database rejection.
The failed request returns 500, does not change the tenant, and does not call the provider API.
After the permission is restored, the same signed event returns 200 and activates the plan.
A duplicate delivery returns 200 without another ledger row or provider request.
PostgreSQL then terminates the application's open connections.
The check sends a new signed event through a terminated connection.
The request returns 500 and does not call the provider API.
The next delivery must return 200, activate the plan, and create one ledger row.
This check detects a ledger row that suppresses an incomplete event.
The provider fetch uses a local HTTP server, not the Razorpay API.
The check does not start the complete wpmgr application or its unrelated services.
The source requires Go 1.26.3, so Go can download that toolchain.
