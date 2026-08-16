//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/suisbuds/rc_abc/internal/delivery/httpclient"
	"github.com/suisbuds/rc_abc/internal/httpapi"
	"github.com/suisbuds/rc_abc/internal/notification"
	"github.com/suisbuds/rc_abc/internal/store/postgres"
	"github.com/suisbuds/rc_abc/internal/worker"
	"go.uber.org/zap"
)

const localAPIToken = "local-e2e-api-token"

func TestLocalNotificationFlowRetriesUntilSuccess(t *testing.T) {
	ctx := context.Background()
	pool := localPool(t)
	if _, err := pool.Exec(ctx, "TRUNCATE notification_tasks"); err != nil {
		t.Fatalf("truncate notification_tasks: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "TRUNCATE notification_tasks"); err != nil {
			t.Errorf("clean notification_tasks: %v", err)
		}
	})

	var receiverCalls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := receiverCalls.Add(1)
		var body map[string]string
		err := json.NewDecoder(request.Body).Decode(&body)
		if err != nil || request.Method != http.MethodPost || request.Header.Get("X-Test-Token") != "fake-receiver-token" ||
			body["event_id"] != "evt-local-e2e" {
			http.Error(w, "invalid delivered request", http.StatusBadRequest)
			return
		}
		if call < 3 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	cipher := localHeaderCipher(t)
	repository := postgres.NewNotificationRepository(pool, cipher)
	service := notification.NewService(repository, true)
	logger := zap.NewNop()
	api := httptest.NewServer(httpapi.NewRouter(logger, pool.Ping, localAPIToken, service))
	t.Cleanup(api.Close)

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	notificationWorker := worker.New(worker.Config{
		Repository: repository, Deliverer: httpclient.New(time.Second), Logger: logger,
		Concurrency: 1, PollInterval: 5 * time.Millisecond, LeaseDuration: 2 * time.Second,
		DeliveryTimeout: time.Second, MaxAttempts: 3,
		Backoff: worker.Backoff{Base: 10 * time.Millisecond, Max: 20 * time.Millisecond},
		Random:  func() float64 { return 0.5 },
	})
	workerDone := make(chan struct{})
	go func() {
		notificationWorker.Run(workerCtx)
		close(workerDone)
	}()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-workerDone:
		case <-time.After(2 * time.Second):
			t.Error("worker did not stop within the local test timeout")
		}
	})

	localClient := &http.Client{Timeout: time.Second}
	created := createNotification(t, localClient, api.URL, receiver.URL)
	if created.Status != notification.StatusPending || created.AttemptCount != 0 {
		t.Fatalf("create response = %+v, want pending at attempt 0", created)
	}

	completed := waitForTerminalTask(t, localClient, api.URL, created.ID)
	if completed.Status != notification.StatusSucceeded || completed.AttemptCount != 3 {
		t.Fatalf("completed task = %+v, want succeeded after 3 attempts", completed)
	}
	if completed.LastHTTPStatus == nil || *completed.LastHTTPStatus != http.StatusOK {
		t.Fatalf("last HTTP status = %v, want 200", completed.LastHTTPStatus)
	}
	if receiverCalls.Load() != 3 {
		t.Fatalf("receiver calls = %d, want 3", receiverCalls.Load())
	}
}

type taskResponse struct {
	ID             uuid.UUID           `json:"id"`
	Status         notification.Status `json:"status"`
	AttemptCount   int                 `json:"attempt_count"`
	LastHTTPStatus *int                `json:"last_http_status"`
}

func createNotification(t *testing.T, client *http.Client, apiURL, targetURL string) taskResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"target_url": targetURL,
		"headers":    map[string]string{"X-Test-Token": "fake-receiver-token"},
		"body":       map[string]string{"event_id": "evt-local-e2e"},
	})
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, apiURL+"/v1/notifications", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+localAPIToken)
	request.Header.Set("Idempotency-Key", "local:e2e:notification:001")
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create status = %d, body = %s", response.StatusCode, body)
	}
	return decodeTask(t, response.Body)
}

func waitForTerminalTask(t *testing.T, client *http.Client, apiURL string, id uuid.UUID) taskResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/notifications/%s", apiURL, id), nil)
		if err != nil {
			t.Fatalf("build get request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+localAPIToken)
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("get notification: %v", err)
		}
		task := decodeTask(t, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get status = %d", response.StatusCode)
		}
		if task.Status == notification.StatusSucceeded || task.Status == notification.StatusDead {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("notification did not reach a terminal state")
	return taskResponse{}
}

func decodeTask(t *testing.T, reader io.Reader) taskResponse {
	t.Helper()
	var task taskResponse
	if err := json.NewDecoder(reader).Decode(&task); err != nil {
		t.Fatalf("decode task response: %v", err)
	}
	return task
}

func localPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("RC_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://rc:rc@localhost:5432/rc?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to local PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func localHeaderCipher(t *testing.T) *postgres.HeaderCipher {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = 9
	}
	cipher, err := postgres.NewHeaderCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("create local header cipher: %v", err)
	}
	return cipher
}
