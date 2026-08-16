# rc_abc

`rc_abc` is a minimal internal service for accepting JSON HTTP notification jobs and delivering them reliably to external HTTP(S) endpoints.

## Status

The service accepts authenticated JSON notification jobs, persists them idempotently in PostgreSQL, encrypts target headers with AES-256-GCM, and delivers them asynchronously with bounded retries. PostgreSQL leases and `FOR UPDATE SKIP LOCKED` coordinate workers and recover abandoned tasks; task status is available through the API.

## Prerequisites

- Go 1.26.6 or newer
- Docker-compatible container runtime

## Quick start

```bash
cp .env.example .env
make setup
make up
make migrate
make run
```

Check the process:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Create a notification:

```bash
curl -i http://localhost:8080/v1/notifications \
  -H 'Authorization: Bearer replace-me' \
  -H 'Idempotency-Key: billing:payment:12345' \
  -H 'Content-Type: application/json' \
  -d '{
    "target_url": "https://receiver.example/events",
    "headers": {"Authorization": "Bearer supplier-token"},
    "body": {"event_id": "evt-123"}
  }'
```

The first accepted request returns `202`. Replaying the same request returns the existing task with `200`; changing the request while reusing the key returns `409`.

Query the returned task ID until it reaches `succeeded` or `dead`:

```bash
curl -H 'Authorization: Bearer replace-me' \
  http://localhost:8080/v1/notifications/00000000-0000-0000-0000-000000000000
```

Delivery treats `2xx` as success. Network errors, timeouts, `408`, `429`, and `5xx` are retried with exponential backoff and jitter; other responses and exhausted retries end in `dead`.

## Quality checks

```bash
make test
make lint
make verify
```

## Local acceptance test

Run the complete notification flow against the Compose PostgreSQL instance:

```bash
make test-e2e
```

The test starts PostgreSQL, applies migrations, runs the API and worker in process, and uses a local receiver that returns `503` twice and `200` on the third attempt. It verifies the final `succeeded` status and attempt count without accessing the public internet. Run `make down` afterward to stop PostgreSQL.
