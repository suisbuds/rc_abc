package httpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestClientDeliverClassifiesInvalidRequestAsPermanent(t *testing.T) {
	outcome := New(time.Second).Deliver(context.Background(), notification.Task{
		TargetURL: "://invalid",
		Method:    http.MethodPost,
		Body:      json.RawMessage(`{}`),
	})
	if outcome.Kind != OutcomePermanentFailure || outcome.ErrorCode != ErrorInvalidRequest {
		t.Fatalf("Deliver() = %+v, want permanent invalid request", outcome)
	}
}

func TestClientDeliverDoesNotFollowRedirects(t *testing.T) {
	var redirectTargetCalled atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled.Store(true)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, redirectTarget.URL, http.StatusFound)
	}))
	defer redirectSource.Close()

	outcome := New(time.Second).Deliver(context.Background(), notification.Task{
		TargetURL: redirectSource.URL,
		Method:    http.MethodPost,
		Body:      json.RawMessage(`{}`),
	})
	if redirectTargetCalled.Load() {
		t.Fatal("Deliver() followed a redirect to another target")
	}
	if outcome.Kind != OutcomePermanentFailure || outcome.HTTPStatus != http.StatusFound {
		t.Fatalf("Deliver() = %+v, want permanent redirect failure", outcome)
	}
}

func TestClientDeliverReadsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	outcome := New(time.Second).Deliver(context.Background(), notification.Task{
		TargetURL: server.URL,
		Method:    http.MethodPost,
		Body:      json.RawMessage(`{}`),
	})
	if outcome.Kind != OutcomeRetryable || outcome.RetryAfter != 2*time.Minute {
		t.Fatalf("Deliver() = %+v, want retryable after 2m", outcome)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "seconds", value: "30", want: 30 * time.Second},
		{name: "http date", value: now.Add(45 * time.Second).Format(http.TimeFormat), want: 45 * time.Second},
		{name: "expired date", value: now.Add(-time.Second).Format(http.TimeFormat)},
		{name: "invalid", value: "later"},
		{name: "negative", value: "-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseRetryAfter(test.value, now); got != test.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", test.value, got, test.want)
			}
		})
	}
}
