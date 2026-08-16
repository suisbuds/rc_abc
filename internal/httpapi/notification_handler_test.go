package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/zap"
)

func TestCreateNotification(t *testing.T) {
	task := responseTestTask()
	service := &stubNotificationService{
		create: func(_ context.Context, request notification.CreateRequest) (notification.Task, bool, error) {
			if request.IdempotencyKey != "billing:payment:12345" {
				t.Fatalf("IdempotencyKey = %q", request.IdempotencyKey)
			}
			return task, true, nil
		},
	}
	router := NewRouter(zap.NewNop(), successfulReadiness, "internal-token", service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/notifications", strings.NewReader(`{
        "target_url":"https://receiver.test/events",
        "headers":{"Authorization":"Bearer supplier-secret"},
        "body":{"event_id":"evt-123"}
    }`))
	request.Header.Set("Authorization", "Bearer internal-token")
	request.Header.Set("Idempotency-Key", "billing:payment:12345")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	var payload notificationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ID != task.ID || payload.Status != notification.StatusPending {
		t.Fatalf("response = %+v", payload)
	}
}

func TestCreateNotificationRejectsUnauthorizedRequest(t *testing.T) {
	service := &stubNotificationService{
		create: func(_ context.Context, _ notification.CreateRequest) (notification.Task, bool, error) {
			t.Fatal("Create() called for unauthorized request")
			return notification.Task{}, false, nil
		},
	}
	router := NewRouter(zap.NewNop(), successfulReadiness, "internal-token", service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/notifications", strings.NewReader(`{"target_url":"https://receiver.test","body":{}}`))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestCreateNotificationReturnsConflict(t *testing.T) {
	service := &stubNotificationService{
		create: func(_ context.Context, _ notification.CreateRequest) (notification.Task, bool, error) {
			return notification.Task{}, false, notification.ErrIdempotencyConflict
		},
	}
	router := NewRouter(zap.NewNop(), successfulReadiness, "internal-token", service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/notifications", strings.NewReader(`{"target_url":"https://receiver.test","body":{}}`))
	request.Header.Set("Authorization", "Bearer internal-token")
	request.Header.Set("Idempotency-Key", "billing:payment:12345")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestGetNotificationReturnsNotFound(t *testing.T) {
	service := &stubNotificationService{
		get: func(_ context.Context, _ uuid.UUID) (notification.Task, error) {
			return notification.Task{}, notification.ErrNotFound
		},
	}
	router := NewRouter(zap.NewNop(), successfulReadiness, "internal-token", service)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/notifications/"+uuid.NewString(), nil)
	request.Header.Set("Authorization", "Bearer internal-token")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

type stubNotificationService struct {
	create func(context.Context, notification.CreateRequest) (notification.Task, bool, error)
	get    func(context.Context, uuid.UUID) (notification.Task, error)
}

func (s *stubNotificationService) Create(ctx context.Context, request notification.CreateRequest) (notification.Task, bool, error) {
	if s.create == nil {
		return notification.Task{}, false, errors.New("unexpected Create call")
	}
	return s.create(ctx, request)
}

func (s *stubNotificationService) Get(ctx context.Context, id uuid.UUID) (notification.Task, error) {
	if s.get == nil {
		return notification.Task{}, errors.New("unexpected Get call")
	}
	return s.get(ctx, id)
}

func successfulReadiness(_ context.Context) error { return nil }

func responseTestTask() notification.Task {
	now := time.Now().UTC()
	return notification.Task{
		ID:            uuid.New(),
		TargetURL:     "https://receiver.test/events",
		Status:        notification.StatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextAttemptAt: now,
	}
}
