# RC ABC agent guide

## Goal

Build a small internal service that accepts JSON HTTP notification jobs and delivers them reliably to external HTTP(S) endpoints.

## Required reading

- Read `docs/design.md` before changing architecture, delivery semantics, state transitions, persistence, or retry behavior.
- Use `.agents/skills/rc-mvp/SKILL.md` for implementation and verification tasks.

## Language

- Write Go code, comments, configuration, automation, commit messages, and repository documentation outside `docs/` in English.
- Write all content under `docs/` in Chinese.

## Architecture boundaries

- Keep HTTP transport in `internal/httpapi`.
- Keep notification rules and state transitions in `internal/notification`.
- Keep PostgreSQL code in `internal/store/postgres`.
- Keep outbound HTTP calls in `internal/delivery/httpclient`.
- Keep scheduling and lease coordination in `internal/worker`.
- Do not add business-specific supplier adapters, ordering, priorities, message queues, or an ORM without an explicit design decision.

## Development rules

- Prefer small, coherent changes.
- Add tests first for reliability-sensitive logic.
- Use explicit SQL and short transactions.
- Do not perform external HTTP calls inside database transactions.
- Do not add mutable global state.
- Do not create generic `utils`, `common`, or `manager` packages.
- Keep dependencies justified and pinned through `go.mod`.

## Git rules

- Inspect `git status --short` before editing and before handoff.
- Preserve unrelated changes.
- Staging, committing, and pushing are allowed when they are part of the user's requested task; report what was done.
- Never rewrite shared history or run destructive Git commands unless the user explicitly requests it.
- Use Conventional Commits.

## Secret handling

- Never commit real credentials, local `.env` files, private keys, database dumps, or production request samples.
- Never log complete authorization headers, cookies, request bodies, response bodies, or database URLs.
- Use fake credentials in tests and demos.
- Treat detected credential exposure as a rotation problem, not only a Git cleanup problem.

## Documentation

- `docs/design.md` contains the Chinese design Q&A and must distinguish current implementation, deliberate exclusions, and future evolution.
- `docs/help.md` is user-authored. Do not modify it unless explicitly requested.
- Write material task summaries under `docs/session/` using `docs/session/TEMPLATE.md`.
- Session notes are local audit material and must not contain secrets.

## Definition of done

- Relevant tests pass.
- `make verify` passes for code changes.
- PostgreSQL integration tests pass for persistence, migration, locking, or lease changes.
- Generated files are current.
- `git diff --check` is clean.
- The handoff distinguishes executed checks from unexecuted checks.
