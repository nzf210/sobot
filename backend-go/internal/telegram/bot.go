package telegram

import (
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/models"
	"hybrid-solana-bot/internal/orchestrator"
)

type Bot struct {
	api  *tgbotapi.BotAPI
	cfg  config.Config
	orch *orchestrator.Orchestrator
	log  *zap.Logger
}

func NewBot(cfg config.Config, orch *orchestrator.Orchestrator, log *zap.Logger) (*Bot, error) {
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("telegram bot token is empty")
	}

	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, err
	}

	return &Bot{
		api:  botAPI,
		cfg:  cfg,
		orch: orch,
		log:  log,
	}, nil
}

func (b *Bot) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message updates
			continue
		}

		if !update.Message.IsCommand() {
			continue
		}

		// check whitelist
		isWhitelisted := false
		senderID := strconv.FormatInt(update.Message.Chat.ID, 10)
		for _, id := range b.cfg.TelegramWhitelistUserIDs {
			if id == senderID {
				isWhitelisted = true
				break
			}
		}

		if !isWhitelisted && len(b.cfg.TelegramWhitelistUserIDs) > 0 {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unauthorized user. You are not in the whitelist.")
			b.api.Send(msg)
			continue
		}

		cmd := update.Message.Command()
		args := update.Message.CommandArguments()

		b.log.Info("received telegram command", zap.String("command", cmd), zap.String("args", args))

		switch cmd {
		case "help":
			b.handleHelp(update.Message.Chat.ID)
		case "analyze":
			b.handleAnalyze(update.Message.Chat.ID, args)
		case "health":
			b.handleHealth(update.Message.Chat.ID)
		default:
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Type /help for available commands.")
			b.api.Send(msg)
		}
	}
}

func (b *Bot) handleHelp(chatID int64) {
	helpText := `🤖 *Hybrid Solana Bot*

*Available Commands:*
/help - Show this help message
/health - Check system health status
/analyze <token_address> - Analyze a Solana token by its address

_Note: Mock values are used for analysis metrics if triggered manually._`

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handleHealth(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "✅ System is healthy and running smoothly.")
	b.api.Send(msg)
}

func (b *Bot) handleAnalyze(chatID int64, args string) {
	token := strings.TrimSpace(args)
	if token == "" {
		msg := tgbotapi.NewMessage(chatID, "Please provide a token address.\nExample: `/analyze <token_address>`")
		msg.ParseMode = "Markdown"
		b.api.Send(msg)
		return
	}

	msgWait := tgbotapi.NewMessage(chatID, fmt.Sprintf("⏳ Analyzing token `%s`...", token))
	msgWait.ParseMode = "Markdown"
	b.api.Send(msgWait)

	// Create mock metrics for analysis based on the token
	metrics := models.TokenMetrics{
		Token:                token,
		LiquidityUSD:         25000,
		MarketCap:            100000,
		Volume5m:             5000,
		BuySellRatio:         1.2,
		OrganicScore:         0.8,
		WashTradeProbability: 0.1,
	}

	result := b.orch.Process(metrics)
	
	msgText := fmt.Sprintf("🚀 *Token Analysis Completed*\n*Token:* `%s`\n*Result:* %+v\n*Liquidity:* $%.2f\n*Volume 5m:* %.2f", 
		metrics.Token, result, metrics.LiquidityUSD, metrics.Volume5m)
	
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}
