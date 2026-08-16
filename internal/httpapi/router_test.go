package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestHealth(t *testing.T) {
	router := NewRouter(zap.NewNop(), func(_ context.Context) error { return nil }, "test-token", nil)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestReadinessFailure(t *testing.T) {
	router := NewRouter(zap.NewNop(), func(_ context.Context) error { return errors.New("database unavailable") }, "test-token", nil)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestBearerAuthRequiresBearerScheme(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "valid bearer token", authorization: "Bearer test-token", wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", authorization: "bearer test-token", wantStatus: http.StatusNoContent},
		{name: "missing scheme", authorization: "test-token", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic test-token", wantStatus: http.StatusUnauthorized},
		{name: "missing token", authorization: "Bearer ", wantStatus: http.StatusUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/protected", bearerAuth("test-token"), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
