package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hybrid-solana-bot/internal/api"
	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/logger"
)

func main() {
	cfg := config.Load()

	zapLog := logger.New(cfg.LogLevel)
	defer zapLog.Sync()

	server := api.NewServer(cfg, zapLog)

	addr := ":" + cfg.BackendPort
	srv := &http.Server{
		Addr:         addr,
		Handler:      server.Engine(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		zapLog.Info("starting backend on " + addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zapLog.Fatal("server failed: " + err.Error())
		}
	}()

	sig := <-quit
	zapLog.Info("shutting down, signal: " + sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server.Shutdown()

	if err := srv.Shutdown(ctx); err != nil {
		zapLog.Fatal("forced shutdown: " + err.Error())
	}

	zapLog.Info("shutdown complete")
}
