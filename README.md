# rc_abc

## Introduction

Multiple internal business systems need to notify external vendors through HTTP(S) APIs when important events occur. Examples include:

- notifying an advertising platform after a referred user registers;
- notifying a CRM system to update a contact after a subscription payment succeeds;
- notifying an inventory system after a product is purchased.

Each vendor may use a different request URL, header format, and body format. The business systems do not need to consume the external API response; they only need the notification request to be delivered as reliably as possible.

This project implements an internal service that accepts external HTTP notification requests from business systems and delivers them to the requested targets as reliably as possible.

## Prerequisites

- Go 1.26.6 or newer
- Docker-compatible container runtime, such as Docker Desktop or OrbStack

## Quick start and testing

Run the complete single-host end-to-end test:

```bash
make single-test
```

This command starts the real API, PostgreSQL, migrations, workers, HTTP delivery client, and a controlled local supplier receiver. For every scenario it prints the sanitized input, actual HTTP output, assertion, and the capability proved by that result. Terminal output uses colors for input, output, passing assertions, explanations, and failures. Set `NO_COLOR=1` to disable colors or `FORCE_COLOR=1` to preserve them when output is redirected. The command verifies health and readiness checks, authentication, durable task acceptance, two `503` responses followed by a successful retry, task status queries, idempotent replay, idempotency conflicts, request validation, and missing-task handling. The expected final delivery result is `succeeded` with three attempts and a final HTTP status of `200`.

Run the isolated large-scale test:

```bash
make all-test
```

The default run concurrently submits 1,000 unique notification tasks and waits for every task to finish. It prints one sanitized input/output/assertion line for each task, followed by accepted tasks, succeeded tasks, dead tasks, total delivery attempts, elapsed time, and the observed submission rate. Functional counts are asserted; timing and throughput are measured rather than fixed because they depend on the local machine.

The scale can be changed without editing the repository:

```bash
LOAD_TOTAL=5000 LOAD_CONCURRENCY=100 make all-test
```

Both commands leave their isolated Compose environments running for inspection. Stop and remove both test environments and their local data with:

```bash
make test-down
```

The full-chain tests use real internal components and real HTTP requests. Only the external supplier boundary is replaced with a controlled receiver so that failure and success responses are deterministic. Unit tests elsewhere in the repository still use mocks for isolated package behavior.
