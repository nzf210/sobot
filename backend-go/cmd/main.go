package main

import (
    "hybrid-solana-bot/internal/api"
    "hybrid-solana-bot/internal/config"
    "hybrid-solana-bot/internal/logger"
)

func main() {
    cfg := config.Load()

    log := logger.New()

    server := api.NewServer(cfg, log)

    log.Info("starting backend")

    server.Run(":" + cfg.BackendPort)
}