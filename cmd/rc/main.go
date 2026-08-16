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
	"github.com/suisbuds/rc_abc/internal/httpapi"
	"github.com/suisbuds/rc_abc/internal/logging"
	"github.com/suisbuds/rc_abc/internal/migration"
	"github.com/suisbuds/rc_abc/internal/store/postgres"
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

	router := httpapi.NewRouter(logger, pool.Ping)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server started", zap.String("address", cfg.HTTPAddress))
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		logger.Info("http server stopped")
		return nil
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	}
}
