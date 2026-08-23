# Paystable Dashboard

The dashboard is a Vite/React app embedded into the Go binary at build time. It is an ops surface for a single Paystable instance, not a public merchant portal.

## Local Development

```bash
cd dashboard
npm ci
npm run dev
```

Build the embedded assets:

```bash
npm run lint
npm run build
cp -r dist ../internal/ui/dist
```

The release workflow runs the same lint/build step before compiling binaries.

## Runtime Access

When Paystable runs, the embedded dashboard is served at:

```text
http://localhost:8080/dashboard
```

Admin API routes are loopback-only in the backend. Do not expose the dashboard directly to the public internet. Use a VPN, SSH tunnel, or an authenticated reverse proxy if remote access is required.

## Current Views

- Overview: active holds, pending deliveries, exhausted deliveries, rejected webhooks.
- Transactions: searchable hold list and timeline drawer.
- Mismatches: webhook-vs-verified disagreements.
- Deliveries: exhausted callback deliveries and replay action.
- Config: local config visibility and secret rotation actions.
- Verification: deterministic failure search, minimal traces, and baseline results.

## UX Direction

Keep this dashboard dense and operational. It should help an engineer or support person answer:

- What is the transaction state?
- What did the gateway claim?
- What did Paystable verify?
- Was the merchant callback delivered?
- Does this need manual review?

Avoid marketing sections, decorative hero layouts, or copy that implies Paystable is a payment gateway.
