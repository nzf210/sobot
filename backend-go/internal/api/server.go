package api

import (
    "fmt"
    "net/http"

    "github.com/gin-gonic/gin"
    "go.uber.org/zap"

    "hybrid-solana-bot/internal/config"
    "hybrid-solana-bot/internal/models"
    "hybrid-solana-bot/internal/notifier"
    "hybrid-solana-bot/internal/orchestrator"
    "hybrid-solana-bot/internal/telegram"
)

type Server struct {
    router *gin.Engine
    log *zap.Logger
    cfg config.Config
}

func NewServer(cfg config.Config, log *zap.Logger) *Server {

    r := gin.Default()

    s := &Server{
        router: r,
        log: log,
        cfg: cfg,
    }

    orch := orchestrator.New()

    // Start telegram bot
    tgBot, err := telegram.NewBot(cfg, orch, log)
    if err != nil {
        log.Warn("Failed to initialize Telegram Bot", zap.Error(err))
    } else {
        log.Info("Starting Telegram Bot listener")
        go tgBot.Start()
    }

    r.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "status": "ok",
        })
    })

    r.POST("/analyze", func(c *gin.Context) {

        var metrics models.TokenMetrics

        if err := c.ShouldBindJSON(&metrics); err != nil {
            c.JSON(400, gin.H{"error": err.Error()})
            return
        }

        result := orch.Process(metrics)

        // Telegram Notification
        tg := notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs)
        msg := fmt.Sprintf("🚀 *Token Analysis Completed*\n*Token:* `%s`\n*Result:* %+v\n*Liquidity:* $%.2f\n*Volume 5m:* %.2f", 
            metrics.Token, result, metrics.LiquidityUSD, metrics.Volume5m)
        
        go func() {
            if err := tg.SendMessage(msg); err != nil {
                log.Error("failed to send telegram notification", zap.Error(err))
            } else {
                log.Info("telegram notification sent successfully")
            }
        }()

        c.JSON(200, result)
    })

    return s
}

func (s *Server) Run(addr string) {
    s.router.Run(addr)
}