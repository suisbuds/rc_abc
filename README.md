# rc_abc

`rc_abc` is a minimal internal service for accepting JSON HTTP notification jobs and delivering them reliably to external HTTP(S) endpoints.

## Status

The service accepts authenticated JSON notification jobs, persists them idempotently in PostgreSQL, encrypts target headers with AES-256-GCM, and exposes task status. Background HTTP delivery and retries are not implemented yet.

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

## Quality checks

```bash
make test
make lint
make verify
```

## Documentation

- `docs/design.md`: design decisions and trade-offs
- `docs/help.md`: user-authored AI usage statement
- `docs/session/`: local AI task audit notes
