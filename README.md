# rc_abc

`rc_abc` is a minimal internal service for accepting JSON HTTP notification jobs and delivering them reliably to external HTTP(S) endpoints.

## Status

The repository currently contains the engineering harness and service bootstrap. The notification submission and delivery workflow will be implemented in the next development slice.

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
