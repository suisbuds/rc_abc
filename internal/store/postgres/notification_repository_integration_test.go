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

func TestNotificationRepositoryClaimAllowsOnlyOneWorker(t *testing.T) {
	pool := integrationPool(t)
	repository := NewNotificationRepository(pool, integrationHeaderCipher(t))
	created, err := repository.Create(context.Background(), integrationTask("claim:concurrent:123", `{"event_id":"evt-claim"}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	requests := []notification.ClaimRequest{
		{LeaseOwner: "worker-a", Now: now, LeaseUntil: now.Add(time.Minute), MaxAttempts: 3},
		{LeaseOwner: "worker-b", Now: now, LeaseUntil: now.Add(time.Minute), MaxAttempts: 3},
	}
	var claimed atomic.Int32
	var waitGroup sync.WaitGroup
	for _, request := range requests {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			task, ok, claimErr := repository.Claim(context.Background(), request)
			if claimErr != nil {
				t.Errorf("Claim() error = %v", claimErr)
				return
			}
			if ok {
				claimed.Add(1)
				if task.ID != created.Task.ID || task.AttemptCount != 1 {
					t.Errorf("Claim() task = %+v, want created task at attempt 1", task)
				}
			}
		}()
	}
	waitGroup.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claimed count = %d, want 1", claimed.Load())
	}
}

func TestNotificationRepositoryRecoversExpiredLeaseAndRejectsStaleCompletion(t *testing.T) {
	pool := integrationPool(t)
	repository := NewNotificationRepository(pool, integrationHeaderCipher(t))
	created, err := repository.Create(context.Background(), integrationTask("lease:recovery:123", `{"event_id":"evt-lease"}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	first, ok, err := repository.Claim(context.Background(), notification.ClaimRequest{
		LeaseOwner: "worker-old", Now: now, LeaseUntil: now.Add(time.Second), MaxAttempts: 3,
	})
	if err != nil || !ok {
		t.Fatalf("first Claim() = (%+v, %v, %v), want task", first, ok, err)
	}
	second, ok, err := repository.Claim(context.Background(), notification.ClaimRequest{
		LeaseOwner: "worker-new", Now: now.Add(2 * time.Second), LeaseUntil: now.Add(time.Minute), MaxAttempts: 3,
	})
	if err != nil || !ok || second.AttemptCount != 2 {
		t.Fatalf("recovery Claim() = (%+v, %v, %v), want attempt 2", second, ok, err)
	}

	staleUpdated, err := repository.Complete(context.Background(), notification.Completion{
		TaskID: created.Task.ID, LeaseOwner: "worker-old", Status: notification.StatusSucceeded,
		NextAttemptAt: now, UpdatedAt: now,
	})
	if err != nil || staleUpdated {
		t.Fatalf("stale Complete() = (%v, %v), want false without error", staleUpdated, err)
	}
	newUpdated, err := repository.Complete(context.Background(), notification.Completion{
		TaskID: created.Task.ID, LeaseOwner: "worker-new", Status: notification.StatusSucceeded,
		NextAttemptAt: now, UpdatedAt: now,
	})
	if err != nil || !newUpdated {
		t.Fatalf("current Complete() = (%v, %v), want true", newUpdated, err)
	}
}

func TestNotificationRepositoryExpiresLeaseAfterFinalAttempt(t *testing.T) {
	pool := integrationPool(t)
	repository := NewNotificationRepository(pool, integrationHeaderCipher(t))
	created, err := repository.Create(context.Background(), integrationTask("lease:exhausted:123", `{"event_id":"evt-exhausted"}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `UPDATE notification_tasks
		SET status = 'processing', attempt_count = 3, lease_owner = 'crashed-worker', lease_until = $2
		WHERE id = $1`, created.Task.ID, now.Add(-time.Second)); err != nil {
		t.Fatalf("prepare expired task: %v", err)
	}
	_, claimed, err := repository.Claim(context.Background(), notification.ClaimRequest{
		LeaseOwner: "worker-new", Now: now, LeaseUntil: now.Add(time.Minute), MaxAttempts: 3,
	})
	if err != nil || claimed {
		t.Fatalf("Claim() = (%v, %v), want no exhausted task", claimed, err)
	}
	stored, err := repository.Get(context.Background(), created.Task.ID)
	if err != nil || stored.Status != notification.StatusDead || stored.AttemptCount != 3 {
		t.Fatalf("Get() = (%+v, %v), want dead at attempt 3", stored, err)
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
