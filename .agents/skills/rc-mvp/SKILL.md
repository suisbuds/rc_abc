---
name: rc-mvp
description: Implement, review, and verify scoped changes in the rc_abc Go notification service. Use for repository setup, API and worker implementation, PostgreSQL migrations, reliability logic, tests, quality checks, and task handoff summaries in this repository.
---

# RC MVP workflow

## Prepare

1. Read `AGENTS.md` completely.
2. Read `docs/design.md` when it exists and the task affects architecture or behavior.
3. Run `make agent-preflight`.
4. Inspect the relevant implementation and tests before editing.

## Implement

1. Keep the requested scope explicit.
2. Add or update tests first for state transitions, retry behavior, persistence, and other correctness-sensitive logic.
3. Keep handlers, domain logic, persistence, delivery, and worker coordination in their assigned packages.
4. Write code, configuration, comments, commit messages, and repository automation in English.
5. Write content under `docs/` in Chinese.
6. Do not modify `docs/help.md` unless the user explicitly asks.
7. Never place credentials, complete authorization headers, or sensitive request bodies in code, logs, fixtures, or session notes.

## Verify

1. Run targeted tests during implementation.
2. Run `make verify` before handoff unless the task is documentation-only.
3. Run `make test-integration` when PostgreSQL behavior, migrations, transactions, locking, or leases change.
4. Run `make generate-check` when generated mocks or generators change.
5. Inspect `git diff --check` and `git status --short`.
6. Report commands that were not run separately from commands that passed.

## Hand off

1. Summarize files changed, decisions, verification, unverified items, and remaining risks.
2. Create a local Chinese audit note under `docs/session/` from `docs/session/TEMPLATE.md` for material implementation tasks.
3. Stage, commit, or push only when the user requests Git operations for the current task.
4. Preserve unrelated user changes and avoid destructive Git commands.
