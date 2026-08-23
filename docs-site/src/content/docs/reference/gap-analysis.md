---
title: Project Readiness
description: Honest gaps before Paystable should be treated as broad production infrastructure.
---

Paystable has a useful niche, but it should not be oversold. This page tracks the main gaps that still separate it from a broadly trusted payments infrastructure project.

## Gateway Coverage

Current adapter support covers PayU and Razorpay. Razorpay has Test Mode evidence. This coverage is not enough for broad Indian merchant adoption.

Needed:

- Cashfree adapter
- PhonePe adapter
- a cleaner connector test suite for gateway-specific signatures and status semantics

## Integration SDKs

Merchants currently write raw HTTP calls and callback HMAC verification themselves. That is acceptable for early adopters, but fragile for wider use.

Needed:

- Node/TypeScript callback verifier
- Go client package
- Python client package
- framework examples for Express, Next.js, Gin, and FastAPI

## Dashboard Auth and Manual Resolution

The dashboard APIs are loopback-only. That is safer than exposing an unauthenticated admin surface, but it is not a complete remote ops story.

Needed:

- authenticated reverse-proxy guide
- OIDC or signed session support
- manual resolution actions for `MISMATCH` and `INDETERMINATE`
- clearer replay controls and audit notes

## Multi-Tenant Support

The current schema is single-merchant. Agencies or SaaS platforms would need separate deployments per merchant.

Needed:

- `tenant_id` scoping
- database-backed API keys
- per-tenant gateway credentials
- per-tenant callback secrets and dashboard access

## Reconciliation Beyond Online Status

Paystable records evidence, but it is not yet a bank statement reconciliation engine.

Needed:

- CSV import
- settlement report matching
- manual adjustment ledger events
- exported dispute bundles

## Ops Maturity

Current observability is metrics, structured logs, dashboard tables, and alerts. Useful, but not enough for larger deployments.

Needed:

- OpenTelemetry traces
- Grafana dashboard templates
- documented backup/restore procedure
- load-test results and sizing guidance

## Positioning Risk

The biggest product risk is sounding like a generic payment orchestrator. That invites comparison with much larger systems and misses the actual value.

The correct position is narrower:

> Paystable is a small payment truth layer for merchants that already have a gateway and need safer fulfillment decisions.
