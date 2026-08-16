package notification

import (
	"context"

	"github.com/google/uuid"
)

//go:generate ../../.tools/mockgen -source=repository.go -destination=../mocks/notification_repository.go -package=mocks

type Repository interface {
	Create(context.Context, Task) (CreateResult, error)
	Get(context.Context, uuid.UUID) (Task, error)
}
