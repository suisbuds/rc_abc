package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxIdempotencyKeyLength = 128
	maxTargetURLLength      = 2048
	maxHeaderCount          = 32
	maxHeaderBytes          = 16 << 10
	maxBodyBytes            = 256 << 10
)

var (
	ErrInvalidRequest      = errors.New("invalid notification request")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with another request")
	ErrNotFound            = errors.New("notification not found")
)

type Service struct {
	repository Repository
	allowHTTP  bool
}

func NewService(repository Repository, allowHTTP bool) *Service {
	return &Service{repository: repository, allowHTTP: allowHTTP}
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Task, bool, error) {
	normalized, err := s.normalize(request)
	if err != nil {
		return Task{}, false, err
	}

	now := time.Now().UTC()
	task := Task{
		ID:             uuid.New(),
		IdempotencyKey: normalized.IdempotencyKey,
		TargetURL:      normalized.TargetURL,
		Method:         "POST",
		Headers:        normalized.Headers,
		Body:           normalized.Body,
		Status:         StatusPending,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	result, err := s.repository.Create(ctx, task)
	if err != nil {
		return Task{}, false, fmt.Errorf("create notification: %w", err)
	}
	if !result.Created && !result.SameRequest {
		return Task{}, false, ErrIdempotencyConflict
	}
	return result.Task, result.Created, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Task, error) {
	task, err := s.repository.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Service) normalize(request CreateRequest) (CreateRequest, error) {
	if err := validateIdempotencyKey(request.IdempotencyKey); err != nil {
		return CreateRequest{}, err
	}
	targetURL, err := normalizeTargetURL(request.TargetURL, s.allowHTTP)
	if err != nil {
		return CreateRequest{}, err
	}
	headers, err := normalizeHeaders(request.Headers)
	if err != nil {
		return CreateRequest{}, err
	}
	if len(request.Body) == 0 || len(request.Body) > maxBodyBytes || !json.Valid(request.Body) {
		return CreateRequest{}, fmt.Errorf("%w: body must be valid JSON no larger than %d bytes", ErrInvalidRequest, maxBodyBytes)
	}
	if _, exists := headers["Content-Type"]; !exists {
		headers["Content-Type"] = "application/json"
	}
	return CreateRequest{
		IdempotencyKey: request.IdempotencyKey,
		TargetURL:      targetURL,
		Headers:        headers,
		Body:           append(json.RawMessage(nil), request.Body...),
	}, nil
}

func validateIdempotencyKey(key string) error {
	if len(key) < 8 || len(key) > maxIdempotencyKeyLength || strings.TrimSpace(key) != key {
		return fmt.Errorf("%w: Idempotency-Key must contain 8 to %d printable characters", ErrInvalidRequest, maxIdempotencyKeyLength)
	}
	for _, character := range []byte(key) {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("%w: Idempotency-Key contains an unsupported character", ErrInvalidRequest)
		}
	}
	return nil
}

func normalizeTargetURL(rawURL string, allowHTTP bool) (string, error) {
	if rawURL == "" || len(rawURL) > maxTargetURLLength {
		return "", fmt.Errorf("%w: target_url is required and must not exceed %d bytes", ErrInvalidRequest, maxTargetURLLength)
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: target_url must be an absolute HTTP(S) URL without user info or a fragment", ErrInvalidRequest)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Scheme != "https" && (!allowHTTP || parsed.Scheme != "http") {
		return "", fmt.Errorf("%w: target_url scheme is not allowed", ErrInvalidRequest)
	}
	return parsed.String(), nil
}

func normalizeHeaders(input map[string]string) (map[string]string, error) {
	if len(input) > maxHeaderCount {
		return nil, fmt.Errorf("%w: too many target headers", ErrInvalidRequest)
	}
	result := make(map[string]string, len(input)+1)
	totalBytes := 0
	for name, value := range input {
		canonicalName := textproto.CanonicalMIMEHeaderKey(name)
		if !validHeaderName(name) || !validHeaderValue(value) || isForbiddenHeader(canonicalName) {
			return nil, fmt.Errorf("%w: target header %q is not allowed", ErrInvalidRequest, name)
		}
		if _, exists := result[canonicalName]; exists {
			return nil, fmt.Errorf("%w: duplicate target header %q", ErrInvalidRequest, name)
		}
		totalBytes += len(canonicalName) + len(value)
		if totalBytes > maxHeaderBytes {
			return nil, fmt.Errorf("%w: target headers are too large", ErrInvalidRequest)
		}
		result[canonicalName] = value
	}
	return result, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range []byte(name) {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) &&
			(character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for _, character := range []byte(value) {
		if (character < 0x20 && character != '\t') || character == 0x7f {
			return false
		}
	}
	return true
}

func isForbiddenHeader(name string) bool {
	switch name {
	case "Connection", "Content-Length", "Host", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
