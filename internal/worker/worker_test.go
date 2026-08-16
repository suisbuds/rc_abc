package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/suisbuds/rc_abc/internal/delivery/httpclient"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/zap"
)

func TestProcessOneCompletesAccordingToDeliveryOutcome(t *testing.T) {
	tests := []struct {
		name       string
		attempt    int
		outcome    httpclient.Outcome
		wantStatus notification.Status
	}{
		{name: "success", attempt: 1, outcome: httpclient.Outcome{Kind: httpclient.OutcomeSucceeded, HTTPStatus: 204}, wantStatus: notification.StatusSucceeded},
		{name: "retryable", attempt: 2, outcome: httpclient.Outcome{Kind: httpclient.OutcomeRetryable, HTTPStatus: 503, ErrorCode: httpclient.ErrorHTTPStatus}, wantStatus: notification.StatusRetryWait},
		{name: "permanent", attempt: 1, outcome: httpclient.Outcome{Kind: httpclient.OutcomePermanentFailure, HTTPStatus: 400, ErrorCode: httpclient.ErrorHTTPStatus}, wantStatus: notification.StatusDead},
		{name: "retry limit", attempt: 3, outcome: httpclient.Outcome{Kind: httpclient.OutcomeRetryable, HTTPStatus: 503, ErrorCode: httpclient.ErrorHTTPStatus}, wantStatus: notification.StatusDead},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
			repository := &fakeRepository{task: notification.Task{ID: uuid.New(), AttemptCount: test.attempt}, claimed: true}
			worker := New(Config{
				Repository: repository,
				Deliverer:  &fakeDeliverer{outcome: test.outcome},
				Logger:     zap.NewNop(), Concurrency: 1, PollInterval: time.Millisecond,
				LeaseDuration: time.Minute, DeliveryTimeout: time.Second, MaxAttempts: 3,
				Backoff: Backoff{Base: time.Second, Max: time.Minute},
				Now:     func() time.Time { return now }, Random: func() float64 { return 0.5 },
			})

			processed, err := worker.processOne(context.Background(), "worker-1")
			if err != nil || !processed {
				t.Fatalf("processOne() = (%v, %v), want processed without error", processed, err)
			}
			if repository.completion.Status != test.wantStatus {
				t.Fatalf("completion status = %q, want %q", repository.completion.Status, test.wantStatus)
			}
			if test.wantStatus == notification.StatusRetryWait && !repository.completion.NextAttemptAt.Equal(now.Add(2*time.Second)) {
				t.Fatalf("next attempt = %s, want %s", repository.completion.NextAttemptAt, now.Add(2*time.Second))
			}
		})
	}
}

func TestRunStopsClaimingAndDrainsInFlightDelivery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	repository := &fakeRepository{task: notification.Task{ID: uuid.New(), AttemptCount: 1}, claimed: true}
	worker := New(Config{
		Repository: repository,
		Deliverer: &fakeDeliverer{deliver: func(context.Context, notification.Task) httpclient.Outcome {
			close(started)
			<-release
			return httpclient.Outcome{Kind: httpclient.OutcomeSucceeded, HTTPStatus: 200}
		}},
		Logger: zap.NewNop(), Concurrency: 1, PollInterval: time.Millisecond,
		LeaseDuration: time.Minute, DeliveryTimeout: time.Second, MaxAttempts: 3,
		Backoff: Backoff{Base: time.Second, Max: time.Minute},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()
	<-started
	cancel()

	select {
	case <-done:
		t.Fatal("Run() returned before the in-flight delivery completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not drain the in-flight delivery")
	}
}

type fakeRepository struct {
	task       notification.Task
	claimed    bool
	completion notification.Completion
}

func (r *fakeRepository) Claim(context.Context, notification.ClaimRequest) (notification.Task, bool, error) {
	if !r.claimed {
		return notification.Task{}, false, nil
	}
	r.claimed = false
	return r.task, true, nil
}

func (r *fakeRepository) Complete(_ context.Context, completion notification.Completion) (bool, error) {
	r.completion = completion
	return true, nil
}

type fakeDeliverer struct {
	outcome httpclient.Outcome
	deliver func(context.Context, notification.Task) httpclient.Outcome
}

func (d *fakeDeliverer) Deliver(ctx context.Context, task notification.Task) httpclient.Outcome {
	if d.deliver != nil {
		return d.deliver(ctx, task)
	}
	return d.outcome
}
