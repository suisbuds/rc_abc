package notification

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusRetryWait  Status = "retry_wait"
	StatusSucceeded  Status = "succeeded"
	StatusDead       Status = "dead"
)

type Task struct {
	ID             uuid.UUID
	IdempotencyKey string
	TargetURL      string
	Method         string
	Headers        map[string]string
	Body           json.RawMessage
	Status         Status
	AttemptCount   int
	NextAttemptAt  time.Time
	LastHTTPStatus *int
	LastError      *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateRequest struct {
	IdempotencyKey string
	TargetURL      string
	Headers        map[string]string
	Body           json.RawMessage
}

type CreateResult struct {
	Task        Task
	Created     bool
	SameRequest bool
}
