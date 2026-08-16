package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/suisbuds/rc_abc/internal/mocks"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/mock/gomock"
)

func TestServiceCreateBuildsPendingPostTask(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockRepository(controller)
	repository.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, task notification.Task) (notification.CreateResult, error) {
			if task.Method != "POST" {
				t.Fatalf("Method = %q, want POST", task.Method)
			}
			if task.Status != notification.StatusPending {
				t.Fatalf("Status = %q, want pending", task.Status)
			}
			if task.Headers["Content-Type"] != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", task.Headers["Content-Type"])
			}
			return notification.CreateResult{Task: task, Created: true, SameRequest: true}, nil
		},
	)

	service := notification.NewService(repository, true)
	task, created, err := service.Create(context.Background(), notification.CreateRequest{
		IdempotencyKey: "billing:payment:12345",
		TargetURL:      "http://receiver.test/events",
		Headers:        map[string]string{"x-event-type": "payment.succeeded"},
		Body:           json.RawMessage(`{"event_id":"evt-123"}`),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created {
		t.Fatal("Create() created = false, want true")
	}
	if task.ID.String() == "" {
		t.Fatal("Create() task ID is empty")
	}
}

func TestServiceCreateRejectsUnsafeInput(t *testing.T) {
	controller := gomock.NewController(t)
	service := notification.NewService(mocks.NewMockRepository(controller), false)

	tests := []struct {
		name    string
		request notification.CreateRequest
	}{
		{name: "missing idempotency key", request: validRequest("https://receiver.test/events")},
		{name: "plain HTTP", request: withKey(validRequest("http://receiver.test/events"))},
		{name: "URL user info", request: withKey(validRequest("https://user:pass@receiver.test/events"))},
		{name: "hop by hop header", request: withHeader(withKey(validRequest("https://receiver.test/events")), "Connection", "close")},
		{name: "control character in header value", request: withHeader(withKey(validRequest("https://receiver.test/events")), "X-Event-Type", "paid\x00event")},
		{name: "invalid JSON", request: withBody(withKey(validRequest("https://receiver.test/events")), `{`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := service.Create(context.Background(), test.request); !errors.Is(err, notification.ErrInvalidRequest) {
				t.Fatalf("Create() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestServiceCreateReturnsConflictForChangedReplay(t *testing.T) {
	controller := gomock.NewController(t)
	repository := mocks.NewMockRepository(controller)
	repository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(notification.CreateResult{
		Task:        notification.Task{},
		Created:     false,
		SameRequest: false,
	}, nil)

	service := notification.NewService(repository, false)
	_, _, err := service.Create(context.Background(), withKey(validRequest("https://receiver.test/events")))
	if !errors.Is(err, notification.ErrIdempotencyConflict) {
		t.Fatalf("Create() error = %v, want ErrIdempotencyConflict", err)
	}
}

func validRequest(targetURL string) notification.CreateRequest {
	return notification.CreateRequest{TargetURL: targetURL, Body: json.RawMessage(`{"event_id":"evt-123"}`)}
}

func withKey(request notification.CreateRequest) notification.CreateRequest {
	request.IdempotencyKey = "billing:payment:12345"
	return request
}

func withHeader(request notification.CreateRequest, name, value string) notification.CreateRequest {
	request.Headers = map[string]string{name: value}
	return request
}

func withBody(request notification.CreateRequest, body string) notification.CreateRequest {
	request.Body = json.RawMessage(body)
	return request
}
