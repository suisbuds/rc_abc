package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/suisbuds/rc_abc/internal/notification"
)

const maxResponseDrainBytes = 64 << 10

type OutcomeKind string

const (
	OutcomeSucceeded        OutcomeKind = "succeeded"
	OutcomeRetryable        OutcomeKind = "retryable"
	OutcomePermanentFailure OutcomeKind = "permanent_failure"
)

type ErrorCode string

const (
	ErrorInvalidRequest ErrorCode = "invalid_request"
	ErrorTimeout        ErrorCode = "timeout"
	ErrorNetwork        ErrorCode = "network_error"
	ErrorHTTPStatus     ErrorCode = "http_status"
)

type Outcome struct {
	Kind       OutcomeKind
	HTTPStatus int
	ErrorCode  ErrorCode
	RetryAfter time.Duration
}

type Client struct {
	httpClient *http.Client
}

func New(timeout time.Duration) *Client {
	return &Client{httpClient: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c *Client) Deliver(ctx context.Context, task notification.Task) Outcome {
	request, err := http.NewRequestWithContext(ctx, task.Method, task.TargetURL, bytes.NewReader(task.Body))
	if err != nil {
		return Outcome{Kind: OutcomePermanentFailure, ErrorCode: ErrorInvalidRequest}
	}
	for name, value := range task.Headers {
		request.Header.Set(name, value)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return Outcome{Kind: OutcomeRetryable, ErrorCode: ErrorTimeout}
		}
		return Outcome{Kind: OutcomeRetryable, ErrorCode: ErrorNetwork}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseDrainBytes))

	outcome := classifyStatus(response.StatusCode)
	if outcome.Kind == OutcomeRetryable {
		outcome.RetryAfter = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	}
	return outcome
}

func classifyStatus(statusCode int) Outcome {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return Outcome{Kind: OutcomeSucceeded, HTTPStatus: statusCode}
	case statusCode == http.StatusRequestTimeout,
		statusCode == http.StatusTooManyRequests,
		statusCode >= 500:
		return Outcome{Kind: OutcomeRetryable, HTTPStatus: statusCode, ErrorCode: ErrorHTTPStatus}
	default:
		return Outcome{Kind: OutcomePermanentFailure, HTTPStatus: statusCode, ErrorCode: ErrorHTTPStatus}
	}
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 || seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil || !retryAt.After(now) {
		return 0
	}
	return retryAt.Sub(now)
}
