package httpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/suisbuds/rc_abc/internal/notification"
)

func TestClientDeliverClassifiesResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantKind   OutcomeKind
	}{
		{name: "success", statusCode: http.StatusNoContent, wantKind: OutcomeSucceeded},
		{name: "request timeout", statusCode: http.StatusRequestTimeout, wantKind: OutcomeRetryable},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, wantKind: OutcomeRetryable},
		{name: "server failure", statusCode: http.StatusServiceUnavailable, wantKind: OutcomeRetryable},
		{name: "bad request", statusCode: http.StatusBadRequest, wantKind: OutcomePermanentFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", request.Method)
				}
				if request.Header.Get("X-Supplier-Key") != "fake-key" {
					t.Fatalf("supplier header was not forwarded")
				}
				if request.Header.Get("X-RC-Notification-ID") != "" {
					t.Fatalf("unexpected service-owned notification header")
				}
				w.WriteHeader(test.statusCode)
			}))
			defer server.Close()

			client := New(time.Second)
			outcome := client.Deliver(context.Background(), notification.Task{
				TargetURL: server.URL,
				Method:    http.MethodPost,
				Headers:   map[string]string{"X-Supplier-Key": "fake-key"},
				Body:      json.RawMessage(`{"event_id":"evt-1"}`),
			})
			if outcome.Kind != test.wantKind || outcome.HTTPStatus != test.statusCode {
				t.Fatalf("Deliver() = %+v, want kind %q and status %d", outcome, test.wantKind, test.statusCode)
			}
		})
	}
}

func TestClientDeliverClassifiesTimeoutAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	outcome := New(10*time.Millisecond).Deliver(context.Background(), notification.Task{
		TargetURL: server.URL,
		Method:    http.MethodPost,
		Body:      json.RawMessage(`{}`),
	})
	if outcome.Kind != OutcomeRetryable || outcome.ErrorCode != ErrorTimeout {
		t.Fatalf("Deliver() = %+v, want retryable timeout", outcome)
	}
}
