package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vpsdeck/vpsdeck/internal/auth"
	"github.com/vpsdeck/vpsdeck/internal/config"
	"github.com/vpsdeck/vpsdeck/internal/database"
	webserver "github.com/vpsdeck/vpsdeck/internal/web"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to the VPSDeck YAML configuration")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}

	db, err := database.Open(cfg.Paths.Database)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	authService := auth.NewService(db, cfg.Security.SessionLifetime)
	created, err := authService.BootstrapAdmin(
		os.Getenv("VPSDECK_ADMIN_USERNAME"),
		os.Getenv("VPSDECK_ADMIN_PASSWORD"),
	)
	if err != nil {
		logger.Error("bootstrap admin", "error", err)
		os.Exit(1)
	}
	if created {
		logger.Info("created initial administrator", "username", os.Getenv("VPSDECK_ADMIN_USERNAME"))
	}

	handler, err := webserver.New(cfg, db, authService, logger)
	if err != nil {
		logger.Error("create web server", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.Address(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("VPSDeck started", "address", server.Addr, "environment", cfg.App.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("VPSDeck stopped")
}
