package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/suisbuds/rc_abc/internal/notification"
)

const notificationColumns = `
    id, idempotency_key, target_url, method, headers, body, status,
    attempt_count, next_attempt_at, last_http_status, last_error,
    created_at, updated_at`

type NotificationRepository struct {
	pool         *pgxpool.Pool
	headerCipher *HeaderCipher
}

func NewNotificationRepository(pool *pgxpool.Pool, headerCipher *HeaderCipher) *NotificationRepository {
	return &NotificationRepository{pool: pool, headerCipher: headerCipher}
}

func (r *NotificationRepository) Create(ctx context.Context, candidate notification.Task) (notification.CreateResult, error) {
	encryptedHeaders, err := r.headerCipher.Encrypt(candidate.ID, candidate.Headers)
	if err != nil {
		return notification.CreateResult{}, err
	}

	query := `INSERT INTO notification_tasks (
        id, idempotency_key, target_url, method, headers, body, status,
        attempt_count, next_attempt_at, created_at, updated_at
    ) VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11)
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING ` + notificationColumns

	stored, storedEncryptedHeaders, err := scanNotification(r.pool.QueryRow(ctx, query,
		candidate.ID,
		candidate.IdempotencyKey,
		candidate.TargetURL,
		candidate.Method,
		string(encryptedHeaders),
		string(candidate.Body),
		candidate.Status,
		candidate.AttemptCount,
		candidate.NextAttemptAt,
		candidate.CreatedAt,
		candidate.UpdatedAt,
	))
	if err == nil {
		stored.Headers, err = r.headerCipher.Decrypt(stored.ID, storedEncryptedHeaders)
		if err != nil {
			return notification.CreateResult{}, err
		}
		return notification.CreateResult{Task: stored, Created: true, SameRequest: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return notification.CreateResult{}, fmt.Errorf("insert notification task: %w", err)
	}

	return r.loadIdempotencyConflict(ctx, candidate)
}

func (r *NotificationRepository) Get(ctx context.Context, id uuid.UUID) (notification.Task, error) {
	query := `SELECT ` + notificationColumns + ` FROM notification_tasks WHERE id = $1`
	task, encryptedHeaders, err := scanNotification(r.pool.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.Task{}, notification.ErrNotFound
	}
	if err != nil {
		return notification.Task{}, fmt.Errorf("get notification task: %w", err)
	}
	task.Headers, err = r.headerCipher.Decrypt(task.ID, encryptedHeaders)
	if err != nil {
		return notification.Task{}, err
	}
	return task, nil
}

func (r *NotificationRepository) loadIdempotencyConflict(ctx context.Context, candidate notification.Task) (notification.CreateResult, error) {
	query := `SELECT ` + notificationColumns + `,
        (target_url = $2 AND method = $3 AND body = $4::jsonb) AS same_request
        FROM notification_tasks WHERE idempotency_key = $1`

	var sameRequest bool
	stored, encryptedHeaders, err := scanNotificationWithSameRequest(
		r.pool.QueryRow(ctx, query, candidate.IdempotencyKey, candidate.TargetURL, candidate.Method, string(candidate.Body)),
		&sameRequest,
	)
	if err != nil {
		return notification.CreateResult{}, fmt.Errorf("load idempotent notification task: %w", err)
	}
	stored.Headers, err = r.headerCipher.Decrypt(stored.ID, encryptedHeaders)
	if err != nil {
		return notification.CreateResult{}, err
	}
	sameRequest = sameRequest && maps.Equal(stored.Headers, candidate.Headers)
	return notification.CreateResult{Task: stored, Created: false, SameRequest: sameRequest}, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanNotification(row rowScanner) (notification.Task, json.RawMessage, error) {
	return scanNotificationWithSameRequest(row, nil)
}

func scanNotificationWithSameRequest(row rowScanner, sameRequest *bool) (notification.Task, json.RawMessage, error) {
	var task notification.Task
	var encryptedHeaders json.RawMessage
	var status string
	var lastHTTPStatus sql.NullInt32
	var lastError sql.NullString
	destinations := []any{
		&task.ID,
		&task.IdempotencyKey,
		&task.TargetURL,
		&task.Method,
		&encryptedHeaders,
		&task.Body,
		&status,
		&task.AttemptCount,
		&task.NextAttemptAt,
		&lastHTTPStatus,
		&lastError,
		&task.CreatedAt,
		&task.UpdatedAt,
	}
	if sameRequest != nil {
		destinations = append(destinations, sameRequest)
	}
	if err := row.Scan(destinations...); err != nil {
		return notification.Task{}, nil, err
	}
	task.Status = notification.Status(status)
	if lastHTTPStatus.Valid {
		value := int(lastHTTPStatus.Int32)
		task.LastHTTPStatus = &value
	}
	if lastError.Valid {
		value := lastError.String
		task.LastError = &value
	}
	return task, encryptedHeaders, nil
}
