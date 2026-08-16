-- +goose Up
CREATE TABLE notification_tasks (
    id UUID PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    target_url TEXT NOT NULL,
    method TEXT NOT NULL,
    headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    body JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'retry_wait', 'succeeded', 'dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT,
    lease_until TIMESTAMPTZ,
    last_http_status INTEGER,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX notification_tasks_runnable_idx
    ON notification_tasks (next_attempt_at, created_at)
    WHERE status IN ('pending', 'retry_wait');

CREATE INDEX notification_tasks_expired_lease_idx
    ON notification_tasks (lease_until)
    WHERE status = 'processing';

-- +goose Down
DROP TABLE notification_tasks;
