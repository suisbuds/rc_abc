package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/zap"
)

type ReadinessCheck func(context.Context) error

type NotificationService interface {
	Create(context.Context, notification.CreateRequest) (notification.Task, bool, error)
	Get(context.Context, uuid.UUID) (notification.Task, error)
}

func NewRouter(logger *zap.Logger, readiness ReadinessCheck, apiToken string, service NotificationService) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(recoveryMiddleware(logger), requestLogger(logger))

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if err := readiness(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	if service != nil {
		handler := notificationHandler{service: service, logger: logger}
		protected := router.Group("/v1")
		protected.Use(bearerAuth(apiToken))
		protected.POST("/notifications", handler.create)
		protected.GET("/notifications/:id", handler.get)
	}
	return router
}

func bearerAuth(apiToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		scheme, provided, hasCredentials := strings.Cut(c.GetHeader("Authorization"), " ")
		valid := hasCredentials && strings.EqualFold(scheme, "Bearer") && apiToken != "" && provided != "" &&
			subtle.ConstantTimeCompare([]byte(provided), []byte(apiToken)) == 1
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("unauthorized", "valid bearer token required"))
			return
		}
		c.Next()
	}
}

func requestLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Header("X-Request-ID", requestID)

		startedAt := time.Now()
		c.Next()

		logger.Info("http request",
			zap.String("request_id", requestID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}
}

func recoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, _ any) {
		logger.Error("http panic recovered", zap.Stack("stack"))
		c.AbortWithStatusJSON(http.StatusInternalServerError, errorResponse("internal_error", "internal server error"))
	})
}

func errorResponse(code, message string) gin.H {
	return gin.H{"error": gin.H{"code": code, "message": message}}
}
