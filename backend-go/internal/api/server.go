package api

import (
    "fmt"
    "net/http"

    "github.com/gin-gonic/gin"
    "go.uber.org/zap"

    "hybrid-solana-bot/internal/config"
    "hybrid-solana-bot/internal/manager"
    "hybrid-solana-bot/internal/memory"
    "hybrid-solana-bot/internal/metrics"
    "hybrid-solana-bot/internal/notifier"
    "hybrid-solana-bot/internal/orchestrator"
    "hybrid-solana-bot/internal/scanner"
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

    mem := memory.NewMemoryStore("data/memory")
    orch := orchestrator.New(cfg, mem)

    // Start telegram bot
    tgBot, err := telegram.NewBot(cfg, orch, mem, log)
    if err != nil {
        log.Warn("Failed to initialize Telegram Bot", zap.Error(err))
    } else {
        log.Info("Starting Telegram Bot listener")
        go tgBot.Start()
    }

    // Start automatic token scanner
    tokenScanner := scanner.NewScanner(cfg, orch, mem, log)
    go tokenScanner.Start()

    // Start Position Manager
    posManager := manager.New(mem, log)
    go posManager.Start()

    r.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "status": "ok",
        })
    })

    r.POST("/analyze", func(c *gin.Context) {

        var reqData struct {
            Token string `json:"token"`
        }

        if err := c.ShouldBindJSON(&reqData); err != nil {
            c.JSON(400, gin.H{"error": err.Error()})
            return
        }

        if reqData.Token == "" {
            c.JSON(400, gin.H{"error": "token address is required"})
            return
        }

        metricsData, err := metrics.FetchTokenMetrics(reqData.Token)
        if err != nil {
            c.JSON(500, gin.H{"error": fmt.Sprintf("failed to fetch metrics: %v", err)})
            return
        }

        result := orch.Process(metricsData)

        // Telegram Notification
        tg := notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs)
        msg := fmt.Sprintf("🚀 *Token Analysis Completed*\n*Token:* `%s`\n*Result:* %+v\n*Liquidity:* $%.2f\n*Volume 5m:* %.2f", 
            metricsData.Token, result, metricsData.LiquidityUSD, metricsData.Volume5m)
        
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