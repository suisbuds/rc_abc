package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/suisbuds/rc_abc/internal/config"
	"github.com/suisbuds/rc_abc/internal/delivery/httpclient"
	"github.com/suisbuds/rc_abc/internal/httpapi"
	"github.com/suisbuds/rc_abc/internal/logging"
	"github.com/suisbuds/rc_abc/internal/migration"
	"github.com/suisbuds/rc_abc/internal/notification"
	"github.com/suisbuds/rc_abc/internal/store/postgres"
	"github.com/suisbuds/rc_abc/internal/worker"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(os.Args) >= 2 && os.Args[1] == "migrate" {
		if len(os.Args) != 3 {
			return errors.New("usage: rc migrate <up|down|status>")
		}
		return migration.Run(context.Background(), cfg.DatabaseURL, os.Args[2])
	}
	if len(os.Args) >= 2 && os.Args[1] != "serve" {
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err := cfg.ValidateServerSecrets(); err != nil {
		return err
	}
	headerCipher, err := postgres.NewHeaderCipher(cfg.HeaderEncryptionKey)
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	repository := postgres.NewNotificationRepository(pool, headerCipher)
	notificationService := notification.NewService(repository, cfg.AllowHTTPDelivery())
	deliveryClient := httpclient.New(cfg.DeliveryTimeout)
	notificationWorker := worker.New(worker.Config{
		Repository:      repository,
		Deliverer:       deliveryClient,
		Logger:          logger,
		Concurrency:     cfg.WorkerConcurrency,
		PollInterval:    cfg.PollInterval,
		LeaseDuration:   cfg.LeaseDuration,
		DeliveryTimeout: cfg.DeliveryTimeout,
		MaxAttempts:     cfg.MaxAttempts,
		Backoff:         worker.Backoff{Base: cfg.BaseBackoff, Max: cfg.MaxBackoff},
	})
	router := httpapi.NewRouter(logger, pool.Ping, cfg.APIToken, notificationService)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server started", zap.String("address", cfg.HTTPAddress))
		serverErrors <- server.ListenAndServe()
	}()
	workerDone := make(chan struct{})
	go func() {
		logger.Info("notification workers started", zap.Int("concurrency", cfg.WorkerConcurrency))
		notificationWorker.Run(ctx)
		logger.Info("notification workers stopped")
		close(workerDone)
	}()

	select {
	case <-ctx.Done():
		shutdownTimeout := max(10*time.Second, cfg.DeliveryTimeout+5*time.Second)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		if err := waitForWorker(workerDone, shutdownTimeout); err != nil {
			return err
		}
		logger.Info("http server stopped")
		return nil
	case err := <-serverErrors:
		stop()
		shutdownTimeout := max(10*time.Second, cfg.DeliveryTimeout+5*time.Second)
		if drainErr := waitForWorker(workerDone, shutdownTimeout); drainErr != nil {
			return errors.Join(fmt.Errorf("serve http: %w", err), drainErr)
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	}
}

func waitForWorker(done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("drain notification workers: timed out after %s", timeout)
	}
}
