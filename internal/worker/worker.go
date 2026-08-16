package worker

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/suisbuds/rc_abc/internal/delivery/httpclient"
	"github.com/suisbuds/rc_abc/internal/notification"
	"go.uber.org/zap"
)

type Repository interface {
	Claim(context.Context, notification.ClaimRequest) (notification.Task, bool, error)
	Complete(context.Context, notification.Completion) (bool, error)
}

type Deliverer interface {
	Deliver(context.Context, notification.Task) httpclient.Outcome
}

type Config struct {
	Repository      Repository
	Deliverer       Deliverer
	Logger          *zap.Logger
	Concurrency     int
	PollInterval    time.Duration
	LeaseDuration   time.Duration
	DeliveryTimeout time.Duration
	MaxAttempts     int
	Backoff         Backoff
	Now             func() time.Time
	Random          func() float64
}

type Worker struct {
	repository      Repository
	deliverer       Deliverer
	logger          *zap.Logger
	concurrency     int
	pollInterval    time.Duration
	leaseDuration   time.Duration
	deliveryTimeout time.Duration
	maxAttempts     int
	backoff         Backoff
	now             func() time.Time
	random          func() float64
}

func New(config Config) *Worker {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Random == nil {
		config.Random = rand.Float64
	}
	return &Worker{
		repository: config.Repository, deliverer: config.Deliverer, logger: config.Logger,
		concurrency: config.Concurrency, pollInterval: config.PollInterval,
		leaseDuration: config.LeaseDuration, deliveryTimeout: config.DeliveryTimeout,
		maxAttempts: config.MaxAttempts, backoff: config.Backoff,
		now: config.Now, random: config.Random,
	}
}

func (w *Worker) Run(ctx context.Context) {
	var waitGroup sync.WaitGroup
	for index := range w.concurrency {
		waitGroup.Add(1)
		go func(workerIndex int) {
			defer waitGroup.Done()
			w.runOne(ctx, fmt.Sprintf("%s-%d", uuid.NewString(), workerIndex))
		}(index)
	}
	waitGroup.Wait()
}

func (w *Worker) runOne(ctx context.Context, owner string) {
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := w.processOne(ctx, owner)
		if err != nil {
			w.logger.Error("worker iteration failed", zap.String("lease_owner", owner), zap.Error(err))
			if !w.waitForPoll(ctx) {
				return
			}
			continue
		}
		if processed {
			continue
		}
		if !w.waitForPoll(ctx) {
			return
		}
	}
}

func (w *Worker) waitForPoll(ctx context.Context) bool {
	timer := time.NewTimer(w.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (w *Worker) processOne(ctx context.Context, owner string) (bool, error) {
	now := w.now()
	task, claimed, err := w.repository.Claim(ctx, notification.ClaimRequest{
		LeaseOwner: owner, Now: now, LeaseUntil: now.Add(w.leaseDuration), MaxAttempts: w.maxAttempts,
	})
	if err != nil || !claimed {
		return claimed, err
	}

	deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.deliveryTimeout)
	deliveryStartedAt := time.Now()
	outcome := w.deliverer.Deliver(deliveryCtx, task)
	deliveryDuration := time.Since(deliveryStartedAt)
	cancel()

	completedAt := w.now()
	completion := w.completion(task, owner, outcome, completedAt)
	completionCtx, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), w.deliveryTimeout)
	updated, err := w.repository.Complete(completionCtx, completion)
	completionCancel()
	if err != nil {
		return true, fmt.Errorf("complete notification %s: %w", task.ID, err)
	}
	if !updated {
		w.logger.Warn("notification lease was lost before completion", zap.String("notification_id", task.ID.String()))
		return true, nil
	}
	w.logger.Info("notification delivery attempt finished",
		zap.String("notification_id", task.ID.String()),
		zap.String("status", string(completion.Status)),
		zap.String("outcome", string(outcome.Kind)),
		zap.String("error_code", string(outcome.ErrorCode)),
		zap.Int("attempt", task.AttemptCount),
		zap.Int("http_status", outcome.HTTPStatus),
		zap.Duration("delivery_duration", deliveryDuration),
		zap.Duration("retry_after", outcome.RetryAfter),
	)
	return true, nil
}

func (w *Worker) completion(task notification.Task, owner string, outcome httpclient.Outcome, now time.Time) notification.Completion {
	status := notification.StatusSucceeded
	nextAttemptAt := now
	var lastError *string
	if outcome.Kind != httpclient.OutcomeSucceeded {
		errorCode := string(outcome.ErrorCode)
		lastError = &errorCode
		if outcome.Kind == httpclient.OutcomeRetryable && task.AttemptCount < w.maxAttempts {
			status = notification.StatusRetryWait
			delay := w.backoff.Delay(task.AttemptCount, w.random())
			if outcome.RetryAfter > delay {
				delay = outcome.RetryAfter
			}
			nextAttemptAt = now.Add(delay)
		} else {
			status = notification.StatusDead
		}
	}
	var httpStatus *int
	if outcome.HTTPStatus != 0 {
		httpStatus = &outcome.HTTPStatus
	}
	return notification.Completion{
		TaskID: task.ID, LeaseOwner: owner, Status: status, NextAttemptAt: nextAttemptAt,
		HTTPStatus: httpStatus, LastError: lastError, UpdatedAt: now,
	}
}
