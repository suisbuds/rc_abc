package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
	ErrorNone       ErrorCode = ""
	ErrorTimeout    ErrorCode = "timeout"
	ErrorNetwork    ErrorCode = "network_error"
	ErrorHTTPStatus ErrorCode = "http_status"
)

type Outcome struct {
	Kind       OutcomeKind
	HTTPStatus int
	ErrorCode  ErrorCode
}

type Client struct {
	httpClient *http.Client
}

func New(timeout time.Duration) *Client {
	return &Client{httpClient: &http.Client{Timeout: timeout}}
}

func (c *Client) Deliver(ctx context.Context, task notification.Task) Outcome {
	request, err := http.NewRequestWithContext(ctx, task.Method, task.TargetURL, strings.NewReader(string(task.Body)))
	if err != nil {
		return Outcome{Kind: OutcomePermanentFailure, ErrorCode: ErrorNetwork}
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

	return classifyStatus(response.StatusCode)
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
