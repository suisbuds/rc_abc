//go:build integration

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/suisbuds/rc_abc/internal/notification"
)

func TestNotificationRepositoryCreateIsIdempotent(t *testing.T) {
	pool := integrationPool(t)
	repository := NewNotificationRepository(pool, integrationHeaderCipher(t))
	original := integrationTask("billing:payment:12345", `{"event_id":"evt-123"}`)

	created, err := repository.Create(context.Background(), original)
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	if !created.Created || !created.SameRequest {
		t.Fatalf("Create() first result = %+v, want created same request", created)
	}

	replay := original
	replay.ID = uuid.New()
	replayed, err := repository.Create(context.Background(), replay)
	if err != nil {
		t.Fatalf("Create() replay error = %v", err)
	}
	if replayed.Created || !replayed.SameRequest || replayed.Task.ID != original.ID {
		t.Fatalf("Create() replay result = %+v, want original task", replayed)
	}

	changed := replay
	changed.ID = uuid.New()
	changed.Body = json.RawMessage(`{"event_id":"evt-456"}`)
	conflict, err := repository.Create(context.Background(), changed)
	if err != nil {
		t.Fatalf("Create() changed replay error = %v", err)
	}
	if conflict.Created || conflict.SameRequest {
		t.Fatalf("Create() changed replay result = %+v, want conflict", conflict)
	}

	var storedHeaders string
	if err := pool.QueryRow(context.Background(), "SELECT headers::text FROM notification_tasks WHERE id = $1", original.ID).Scan(&storedHeaders); err != nil {
		t.Fatalf("query encrypted headers: %v", err)
	}
	if strings.Contains(storedHeaders, "supplier-secret") {
		t.Fatal("stored headers contain plaintext secret")
	}
}

func TestNotificationRepositoryConcurrentCreateStoresOneTask(t *testing.T) {
	pool := integrationPool(t)
	repository := NewNotificationRepository(pool, integrationHeaderCipher(t))

	const goroutines = 12
	var createdCount atomic.Int32
	ids := make(chan uuid.UUID, goroutines)
	errorsChannel := make(chan error, goroutines)
	var waitGroup sync.WaitGroup
	for range goroutines {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := repository.Create(context.Background(), integrationTask("billing:payment:concurrent", `{"event_id":"evt-concurrent"}`))
			if err != nil {
				errorsChannel <- err
				return
			}
			if result.Created {
				createdCount.Add(1)
			}
			if !result.SameRequest {
				errorsChannel <- notification.ErrIdempotencyConflict
				return
			}
			ids <- result.Task.ID
		}()
	}
	waitGroup.Wait()
	close(ids)
	close(errorsChannel)

	for err := range errorsChannel {
		t.Fatalf("concurrent Create() error = %v", err)
	}
	if createdCount.Load() != 1 {
		t.Fatalf("created count = %d, want 1", createdCount.Load())
	}
	var expected uuid.UUID
	for id := range ids {
		if expected == uuid.Nil {
			expected = id
		}
		if id != expected {
			t.Fatalf("task ID = %s, want %s", id, expected)
		}
	}
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RC_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://rc:rc@localhost:5432/rc?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), "TRUNCATE notification_tasks"); err != nil {
		t.Fatalf("truncate notification_tasks: %v", err)
	}
	return pool
}

func integrationHeaderCipher(t *testing.T) *HeaderCipher {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = 7
	}
	cipher, err := NewHeaderCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewHeaderCipher() error = %v", err)
	}
	return cipher
}

func integrationTask(idempotencyKey, body string) notification.Task {
	now := time.Now().UTC()
	return notification.Task{
		ID:             uuid.New(),
		IdempotencyKey: idempotencyKey,
		TargetURL:      "https://receiver.test/events",
		Method:         "POST",
		Headers:        map[string]string{"Authorization": "Bearer supplier-secret"},
		Body:           json.RawMessage(body),
		Status:         notification.StatusPending,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}
