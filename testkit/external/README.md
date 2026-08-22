# External implementation check

This check executes an independently authored Razorpay webhook handler.
It does not copy the handler into this repository.

## Source

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
The stored payment amount is ₹500.
The captured event amount is ₹0.01.
The pinned handler changes the payment state to succeeded.

This result reproduces `INV-AMOUNT-1` in one external implementation.
It does not measure Scout against a population of external merchants.
