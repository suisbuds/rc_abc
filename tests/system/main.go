package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	apiToken     = "demo-internal-token"
	targetURL    = "http://mockreceiver:8081/events"
	ansiReset    = "\x1b[0m"
	ansiBoldCyan = "\x1b[1;36m"
	ansiBlue     = "\x1b[34m"
	ansiCyan     = "\x1b[36m"
	ansiGreen    = "\x1b[32m"
	ansiYellow   = "\x1b[33m"
	ansiRed      = "\x1b[31m"
)

type taskResponse struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	AttemptCount   int    `json:"attempt_count"`
	LastHTTPStatus *int   `json:"last_http_status"`
}

type response struct {
	statusCode int
	body       []byte
}

type runner struct {
	apiURL           string
	supplierURL      string
	expectedAttempts int
	client           *http.Client
}

func main() {
	mode := flag.String("mode", "single", "test mode: single or all")
	apiURL := flag.String("api-url", "http://localhost:8080", "notification API base URL")
	supplierURL := flag.String("supplier-url", targetURL, "supplier endpoint used for delivery")
	expectedAttempts := flag.Int("expected-attempts", 3, "expected delivery attempts for the single test")
	flag.Parse()
	if *expectedAttempts <= 0 {
		exitWithError(errors.New("expected-attempts must be greater than zero"))
	}

	concurrency := 50
	var err error
	if *mode == "all" {
		concurrency, err = positiveEnvInt("LOAD_CONCURRENCY", concurrency)
		if err != nil {
			exitWithError(err)
		}
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency * 2,
			MaxIdleConnsPerHost: concurrency,
			MaxConnsPerHost:     concurrency,
		},
	}
	testRunner := runner{apiURL: *apiURL, supplierURL: *supplierURL, expectedAttempts: *expectedAttempts, client: client}
	printLegend()

	switch *mode {
	case "single":
		err = testRunner.runAcceptance()
	case "all":
		var total int
		total, err = positiveEnvInt("LOAD_TOTAL", 1000)
		if err == nil {
			err = testRunner.runLoad(total, concurrency)
		}
	default:
		err = fmt.Errorf("unsupported mode %q", *mode)
	}
	if err != nil {
		exitWithError(err)
	}
}

func (r runner) runAcceptance() error {
	if err := r.expectStatus("health check", "the public liveness endpoint confirms the service process is reachable", http.MethodGet, "/healthz", "", nil, http.StatusOK, false); err != nil {
		return err
	}
	if err := r.expectStatus("readiness check", "the public readiness endpoint confirms the service can reach PostgreSQL", http.MethodGet, "/readyz", "", nil, http.StatusOK, false); err != nil {
		return err
	}
	if err := r.expectStatus("authentication", "protected APIs reject requests without a bearer token", http.MethodGet, "/v1/notifications/00000000-0000-0000-0000-000000000000", "", nil, http.StatusUnauthorized, false); err != nil {
		return err
	}

	key := fmt.Sprintf("acceptance:%d", time.Now().UnixNano())
	payload := notificationPayload(r.supplierURL, "evt-single")
	printCase("durable task acceptance")
	printInput(http.MethodPost, "/v1/notifications", key, payload, true)
	createdResponse, err := r.request(http.MethodPost, "/v1/notifications", key, payload, true)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	printOutput(createdResponse)
	if createdResponse.statusCode != http.StatusAccepted {
		return unexpectedStatus("create notification", createdResponse, http.StatusAccepted)
	}
	created, err := decodeTask(createdResponse.body)
	if err != nil {
		return fmt.Errorf("decode created task: %w", err)
	}
	if created.ID == "" || created.Status != "pending" || created.AttemptCount != 0 {
		return fmt.Errorf("create notification returned unexpected task: %+v", created)
	}
	printPass("HTTP 202, non-empty task ID, pending status, and zero attempts", "the task was durably accepted before asynchronous delivery")

	printCase("retry delivery")
	fmt.Printf("%s poll GET /v1/notifications/%s until terminal state\n", paint(ansiBlue, "INPUT "), created.ID)
	completed, err := r.waitForTerminal(created.ID, 15*time.Second, true)
	if err != nil {
		return err
	}
	if completed.Status != "succeeded" || completed.AttemptCount != r.expectedAttempts || completed.LastHTTPStatus == nil || *completed.LastHTTPStatus < 200 || *completed.LastHTTPStatus >= 300 {
		return fmt.Errorf("retry delivery returned unexpected task: %+v", completed)
	}
	printPass(fmt.Sprintf("status=succeeded, attempts=%d, and final HTTP status is 2xx", r.expectedAttempts), "temporary supplier failures are retried and eventually completed")

	printCase("idempotent replay")
	printInput(http.MethodPost, "/v1/notifications", key, payload, true)
	replayResponse, err := r.request(http.MethodPost, "/v1/notifications", key, payload, true)
	if err != nil {
		return fmt.Errorf("replay notification: %w", err)
	}
	printOutput(replayResponse)
	if replayResponse.statusCode != http.StatusOK {
		return unexpectedStatus("replay notification", replayResponse, http.StatusOK)
	}
	replayed, err := decodeTask(replayResponse.body)
	if err != nil || replayed.ID != created.ID {
		return fmt.Errorf("idempotent replay returned a different task: id=%q error=%v", replayed.ID, err)
	}
	printPass("HTTP 200 and the original task ID is returned", "repeating the same request does not create a duplicate task")

	conflictPayload := notificationPayload(r.supplierURL, "evt-conflict")
	if err := r.expectStatus("idempotency conflict", "one idempotency key cannot silently represent different requests", http.MethodPost, "/v1/notifications", key, conflictPayload, http.StatusConflict, true); err != nil {
		return err
	}
	invalidPayload := notificationPayload("ftp://receiver.invalid/events", "evt-invalid")
	if err := r.expectStatus("request validation", "unsupported supplier URL schemes are rejected before persistence", http.MethodPost, "/v1/notifications", key+":invalid", invalidPayload, http.StatusBadRequest, true); err != nil {
		return err
	}
	if err := r.expectStatus("missing task", "querying an unknown valid task ID returns a clear not-found result", http.MethodGet, "/v1/notifications/00000000-0000-0000-0000-000000000000", "", nil, http.StatusNotFound, true); err != nil {
		return err
	}

	fmt.Println(paint(ansiGreen, "SINGLE TEST PASS checks=9"))
	return nil
}

func (r runner) runLoad(total, concurrency int) error {
	prefix := fmt.Sprintf("load:%d", time.Now().UnixNano())
	fmt.Println("\n" + paint(ansiBoldCyan, "=== large-scale input ==="))
	fmt.Printf("%s total_tasks=%d submit_concurrency=%d supplier=%s\n", paint(ansiBlue, "INPUT "), total, concurrency, r.supplierURL)
	fmt.Println(paint(ansiGreen, "ASSERT every create must return HTTP 202 and every task must finish as succeeded with HTTP 200"))

	startedAt := time.Now()
	ids, err := r.createLoadTasks(prefix, total, concurrency)
	if err != nil {
		return err
	}
	acceptedIn := time.Since(startedAt)

	var totalAttempts atomic.Int64
	completedTasks := make([]taskResponse, total)
	indexes := make([]int, total)
	for index := range total {
		indexes[index] = index
	}
	completedAt := time.Now()
	if err := runConcurrent(indexes, concurrency, func(index int) error {
		id := ids[index]
		task, waitErr := r.waitForTerminal(id, 2*time.Minute, false)
		if waitErr != nil {
			return waitErr
		}
		if task.Status != "succeeded" || task.LastHTTPStatus == nil || *task.LastHTTPStatus != http.StatusOK {
			return fmt.Errorf("task %s completed unexpectedly: %+v", id, task)
		}
		completedTasks[index] = task
		totalAttempts.Add(int64(task.AttemptCount))
		return nil
	}); err != nil {
		return err
	}
	completedIn := time.Since(completedAt)
	totalDuration := time.Since(startedAt)

	fmt.Println("\n" + paint(ansiBoldCyan, "=== per-task input and output ==="))
	for index, task := range completedTasks {
		fmt.Printf("%s %s event_id=evt-load-%d idempotency_key=%s:%d %s create_http=202 task_id=%s status=%s attempts=%d last_http_status=%d %s\n",
			paint(ansiBoldCyan, fmt.Sprintf("TASK %04d", index+1)), paint(ansiBlue, "INPUT"), index, prefix, index,
			paint(ansiCyan, "OUTPUT"), task.ID, task.Status, task.AttemptCount, *task.LastHTTPStatus, paint(ansiGreen, "ASSERT=PASS"))
	}

	fmt.Println("\n" + paint(ansiBoldCyan, "=== large-scale result ==="))
	fmt.Printf("%s total=%d accepted=%d succeeded=%d dead=0 total_delivery_attempts=%d\n", paint(ansiCyan, "OUTPUT"), total, total, total, totalAttempts.Load())
	fmt.Printf("%s accepted_in=%s completed_in=%s total_duration=%s submit_rate=%.1f_tasks_per_second\n", paint(ansiCyan, "OUTPUT"),
		acceptedIn.Round(time.Millisecond), completedIn.Round(time.Millisecond), totalDuration.Round(time.Millisecond),
		float64(total)/acceptedIn.Seconds())
	fmt.Printf("%s\n", paint(ansiGreen, fmt.Sprintf("ASSERT accepted=%d succeeded=%d dead=0: PASS", total, total)))
	fmt.Println(paint(ansiYellow, "VERIFIES this run completed concurrent submission and delivery without losing or killing any task"))
	fmt.Println(paint(ansiGreen, fmt.Sprintf("ALL TEST PASS total=%d concurrency=%d succeeded=%d dead=0", total, concurrency, total)))
	return nil
}

func (r runner) createLoadTasks(prefix string, total, concurrency int) ([]string, error) {
	indexes := make([]int, total)
	for index := range total {
		indexes[index] = index
	}
	ids := make([]string, total)
	err := runConcurrent(indexes, concurrency, func(index int) error {
		key := fmt.Sprintf("%s:%d", prefix, index)
		payload := notificationPayload(r.supplierURL, fmt.Sprintf("evt-load-%d", index))
		createdResponse, requestErr := r.request(http.MethodPost, "/v1/notifications", key, payload, true)
		if requestErr != nil {
			return requestErr
		}
		if createdResponse.statusCode != http.StatusAccepted {
			return unexpectedStatus("load create", createdResponse, http.StatusAccepted)
		}
		created, decodeErr := decodeTask(createdResponse.body)
		if decodeErr != nil {
			return decodeErr
		}
		ids[index] = created.ID
		return nil
	})
	return ids, err
}

func runConcurrent[T any](items []T, concurrency int, operation func(T) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := make(chan T)
	errorsChannel := make(chan error, 1)
	var waitGroup sync.WaitGroup
	for range concurrency {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := operation(item); err != nil {
					select {
					case errorsChannel <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
sendLoop:
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	waitGroup.Wait()
	select {
	case err := <-errorsChannel:
		return err
	default:
		return nil
	}
}

func (r runner) waitForTerminal(id string, timeout time.Duration, trace bool) (taskResponse, error) {
	deadline := time.Now().Add(timeout)
	lastState := ""
	for time.Now().Before(deadline) {
		result, err := r.request(http.MethodGet, "/v1/notifications/"+id, "", nil, true)
		if err != nil {
			return taskResponse{}, err
		}
		if result.statusCode != http.StatusOK {
			return taskResponse{}, unexpectedStatus("query task", result, http.StatusOK)
		}
		task, err := decodeTask(result.body)
		if err != nil {
			return taskResponse{}, err
		}
		state := fmt.Sprintf("%s:%d", task.Status, task.AttemptCount)
		if trace && state != lastState {
			fmt.Printf("%s HTTP %d body=%s\n", paint(ansiCyan, "OUTPUT"), result.statusCode, compactJSON(result.body))
			lastState = state
		}
		if task.Status == "succeeded" || task.Status == "dead" {
			return task, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return taskResponse{}, fmt.Errorf("task %s did not complete within %s", id, timeout)
}

func (r runner) expectStatus(name, proof, method, path, key string, body []byte, expected int, authenticated bool) error {
	printCase(name)
	printInput(method, path, key, body, authenticated)
	result, err := r.request(method, path, key, body, authenticated)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	printOutput(result)
	if result.statusCode != expected {
		return unexpectedStatus(name, result, expected)
	}
	printPass(fmt.Sprintf("HTTP status=%d", expected), proof)
	return nil
}

func printCase(name string) {
	fmt.Printf("\n%s\n", paint(ansiBoldCyan, "=== "+name+" ==="))
}

func printInput(method, path, key string, body []byte, authenticated bool) {
	fmt.Printf("%s method=%s path=%s authorization=%s", paint(ansiBlue, "INPUT "), method, path, presence(authenticated))
	if key != "" {
		fmt.Printf(" idempotency_key=%s", key)
	}
	if len(body) > 0 {
		fmt.Printf(" body=%s", compactJSON(body))
	}
	fmt.Println()
}

func printOutput(result response) {
	fmt.Printf("%s HTTP %d body=%s\n", paint(ansiCyan, "OUTPUT"), result.statusCode, compactJSON(result.body))
}

func printPass(assertion, proof string) {
	fmt.Println(paint(ansiGreen, "ASSERT "+assertion+": PASS"))
	fmt.Println(paint(ansiYellow, "VERIFIES "+proof))
}

func printLegend() {
	fmt.Printf("%s %s | %s | %s | %s\n",
		paint(ansiBoldCyan, "COLOR LEGEND:"), paint(ansiBlue, "INPUT"), paint(ansiCyan, "OUTPUT"),
		paint(ansiGreen, "ASSERT/PASS"), paint(ansiYellow, "VERIFIES"))
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func compactJSON(body []byte) string {
	if len(body) == 0 {
		return "<empty>"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return string(body)
	}
	return compact.String()
}

func paint(color, text string) string {
	if !colorEnabled() {
		return text
	}
	return color + text + ansiReset
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (r runner) request(method, path, key string, body []byte, authenticated bool) (response, error) {
	request, err := http.NewRequestWithContext(context.Background(), method, r.apiURL+path, bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+apiToken)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}

	httpResponse, err := r.client.Do(request)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = httpResponse.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, 1<<20))
	if err != nil {
		return response{}, err
	}
	return response{statusCode: httpResponse.StatusCode, body: responseBody}, nil
}

func notificationPayload(destination, eventID string) []byte {
	payload, err := json.Marshal(map[string]any{
		"target_url": destination,
		"headers":    map[string]string{"X-Test-Token": "fake-token"},
		"body":       map[string]string{"event_id": eventID},
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func decodeTask(body []byte) (taskResponse, error) {
	var task taskResponse
	if err := json.Unmarshal(body, &task); err != nil {
		return taskResponse{}, fmt.Errorf("decode task response %q: %w", body, err)
	}
	return task, nil
}

func unexpectedStatus(name string, result response, expected int) error {
	return fmt.Errorf("%s status=%d want=%d body=%s", name, result.statusCode, expected, result.body)
}

func positiveEnvInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func exitWithError(err error) {
	if !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "%s %v\n", paint(ansiRed, "FAIL"), err)
	}
	os.Exit(1)
}
