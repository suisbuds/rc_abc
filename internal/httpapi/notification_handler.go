package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/zap"
)

const maxCreateNotificationRequestBytes = 320 << 10

type notificationHandler struct {
	service NotificationService
	logger  *zap.Logger
}

type createNotificationRequest struct {
	TargetURL string            `json:"target_url"`
	Headers   map[string]string `json:"headers"`
	Body      json.RawMessage   `json:"body"`
}

type notificationResponse struct {
	ID             uuid.UUID           `json:"id"`
	Status         notification.Status `json:"status"`
	TargetURL      string              `json:"target_url"`
	AttemptCount   int                 `json:"attempt_count"`
	NextAttemptAt  time.Time           `json:"next_attempt_at"`
	LastHTTPStatus *int                `json:"last_http_status,omitempty"`
	LastError      *string             `json:"last_error,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func (h notificationHandler) create(c *gin.Context) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, errorResponse("unsupported_media_type", "Content-Type must be application/json"))
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateNotificationRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request createNotificationRequest
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("invalid_request", "request body must be valid JSON"))
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("invalid_request", "request body must contain one JSON object"))
		return
	}

	task, created, err := h.service.Create(c.Request.Context(), notification.CreateRequest{
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		TargetURL:      request.TargetURL,
		Headers:        request.Headers,
		Body:           request.Body,
	})
	if err != nil {
		h.handleServiceError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
		c.Header("Location", "/v1/notifications/"+task.ID.String())
	}
	c.JSON(status, responseFromTask(task))
}

func (h notificationHandler) get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse("invalid_request", "notification ID must be a UUID"))
		return
	}
	task, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, responseFromTask(task))
}

func (h notificationHandler) handleServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, notification.ErrInvalidRequest):
		c.JSON(http.StatusBadRequest, errorResponse("invalid_request", err.Error()))
	case errors.Is(err, notification.ErrIdempotencyConflict):
		c.JSON(http.StatusConflict, errorResponse("idempotency_conflict", "Idempotency-Key is already used by another request"))
	case errors.Is(err, notification.ErrNotFound):
		c.JSON(http.StatusNotFound, errorResponse("not_found", "notification not found"))
	default:
		h.logger.Error("notification request failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, errorResponse("internal_error", "internal server error"))
	}
}

func responseFromTask(task notification.Task) notificationResponse {
	return notificationResponse{
		ID:             task.ID,
		Status:         task.Status,
		TargetURL:      task.TargetURL,
		AttemptCount:   task.AttemptCount,
		NextAttemptAt:  task.NextAttemptAt,
		LastHTTPStatus: task.LastHTTPStatus,
		LastError:      task.LastError,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected second JSON value")
		}
		return err
	}
	return nil
}
