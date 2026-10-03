# Changelog

## Unreleased — proposed v0.3.0

I prepared these changes for v0.3.0.
The release date remains unset until the release checks pass.

### Local setup

- Add `paystable init` with generated local secrets and private `.env` permissions.
- Refuse to overwrite an existing `.env`.
- Make the installer run `paystable init`.
- Add environment, database, and migration sections to `paystable doctor`.
- Add commands that explain local PostgreSQL connection errors.

### Payment checks and callbacks

- Use the PayU `verify_payment` API with its request hash and nested response format.
- Read the PayU amount from `amt`, `transaction_amount`, or `amount`.
- Report malformed amounts and responses as errors.
- Treat PayU `Not Found` results as unresolved evidence.
- Reject duplicate hold requests when the hold fields conflict.
- Add callbacks for manual review decisions.
- Record manual review decisions in the audit ledger.

### Dashboard and operations

- Build transaction timelines from stored evidence.
- Make the dashboard configuration page read-only.
- Derive dashboard counts from backend data.
- Restrict Docker dashboard access to host loopback and the configured bridge gateway.
- Check embedded dashboard assets in CI.
- Remove the unused hold scanner.

### Pending Week 1 branches

These changes need separate PR review before v0.3.0 includes them.

- Exclude key files and local artifacts from Git and Docker build contexts.
- Explain PayU-only support and the source install path in the README.
- Direct the installer to `samithreddychinni/paystable` release assets.
- Check the local installer with a release fixture.

## v0.2.4 — 2026-06-25

I checked the local tag source and the GitHub release date for this entry.
The release includes `paystable doctor`, but it does not include `paystable init`.

- Explain PostgreSQL ident and peer authentication errors.
- Document local PostgreSQL setup.
- Preserve early gateway webhooks and deduplicate gateway events.
- Require gateway evidence before a terminal payment result.
- Verify installer downloads with release checksums.
- Add contribution and security policy files.
