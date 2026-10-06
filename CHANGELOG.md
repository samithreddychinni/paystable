# changelog

## unreleased

- breaking: replace body-only callback signatures with `v2=` signatures that authenticate the delivery timestamp, event key, and raw body. Update merchant verifiers when upgrading; legacy signatures are rejected.
- reject callback timestamps outside a five-minute clock-skew window and keep the event key stable across retries.

## v0.3.0 (2026-10-03)

- add `paystable init` with generated secrets and private configuration permissions.
- refuse to overwrite an existing configuration.
- run `paystable init` from the installer.
- improve `paystable doctor` with environment, database, and migration checks.
- explain PostgreSQL connection and authentication errors.
- serialize database migrations with a PostgreSQL advisory lock.
- use the PayU `verify_payment` API with its request hash and nested response format.
- read PayU amounts from `amt`, `transaction_amount`, or `amount`.
- report invalid amounts and responses as errors.
- treat PayU `Not Found` results as unresolved evidence.
- reject conflicting duplicate hold requests.
- send callbacks for manual review decisions and record them in the ledger.
- build transaction timelines from stored evidence.
- make dashboard configuration read-only and use backend data for counts.
- restrict Docker dashboard access to host loopback and the configured bridge gateway.
- check embedded dashboard assets in CI.
- remove the unused hold scanner.
