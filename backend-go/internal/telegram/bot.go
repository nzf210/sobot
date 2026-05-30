package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/metrics"
	"hybrid-solana-bot/internal/orchestrator"
)

type Bot struct {
	api  *tgbotapi.BotAPI
	cfg  config.Config
	orch *orchestrator.Orchestrator
	mem  *memory.MemoryStore
	log  *zap.Logger
}

func NewBot(cfg config.Config, orch *orchestrator.Orchestrator, mem *memory.MemoryStore, log *zap.Logger) (*Bot, error) {
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
		mem:  mem,
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
		case "config":
			userCfg := b.mem.GetUserConfig()
			cfgJSON, _ := json.MarshalIndent(userCfg, "", "  ")
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "⚙️ *Current Bot Config:*\n```json\n"+string(cfgJSON)+"\n```")
			msg.ParseMode = "Markdown"
			b.api.Send(msg)
		case "setconfig":
			args := strings.Split(update.Message.CommandArguments(), " ")
			if len(args) < 2 {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "❌ Usage: `/setconfig <key> <value>`")
				msg.ParseMode = "Markdown"
				b.api.Send(msg)
				continue
			}
			key := args[0]
			valStr := args[1]

			// Basic type parsing (bool and float)
			var val interface{} = valStr
			if valStr == "true" {
				val = true
			} else if valStr == "false" {
				val = false
			} else if f, err := strconv.ParseFloat(valStr, 64); err == nil {
				val = f
			}

			err := b.mem.UpdateUserConfig(key, val)
			if err != nil {
				b.api.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "❌ Failed to update config: "+err.Error()))
			} else {
				b.api.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "✅ Config updated successfully!"))
			}
		default:
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /analyze <token>, /config, or /setconfig.")
			b.api.Send(msg)
		}
	}
}

func (b *Bot) handleHelp(chatID int64) {
	helpText := `🤖 *Hybrid Solana Bot*

*Daftar Perintah (Commands):*
/help - Menampilkan pesan panduan ini
/health - Cek status kesehatan sistem bot
/analyze <token_address> - Melakukan analisa instan pada sebuah koin (bypass scanner)
/config - Menampilkan konfigurasi bot (Risk Engine & Trade Settings)
/setconfig <key> <value> - Mengubah konfigurasi bot. Contoh: 
   - /setconfig minMcapSOL 500
   - /setconfig autoTrade false

*Cara Kerja Bot:*
1. *Scanner:* Memindai koin baru di Solana via DexScreener sesuai interval.
2. *Risk Engine:* Menyaring token yang tidak sesuai batas di /config.
3. *AI LLM:* Menganalisa narasi dan metrik (dibantu oleh memori masa lalu / lessons.json).
4. *Executor:* Jika keputusan AI = BUY, bot otomatis menembak Jup.ag dan membeli sebesar 'maxDeployAmountSol'.
5. *Position Manager:* Otomatis memantau posisi terbuka dan menjual bila 'takeProfitPct' atau 'stopLossPct' tercapai, lalu mencatat hasilnya agar AI semakin cerdas (Self-Learning).

*Parameter Config Penting:*
- *autoTrade*: true/false (mengaktifkan auto beli)
- *scannerIntervalSec*: Interval waktu scanner (detik)
- *minLiquiditySOL / maxLiquiditySOL*: Batas Likuiditas (dalam SOL)
- *minMcapSOL / maxMcapSOL*: Batas Market Cap (dalam SOL)
- *minVolumeSOL*: Volume minimal 5 menit terakhir (dalam SOL)
- *maxDeployAmountSol*: Modal maksimum tiap kali snipe (dalam SOL)
- *takeProfitPct / stopLossPct*: Persentase untung/rugi untuk menutup posisi (mis. TP: 20.0, SL: -10.0)`

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

	// Fetch real metrics from DexScreener
	metricsData, err := metrics.FetchTokenMetrics(token)
	if err != nil {
		msgErr := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Error fetching metrics: %v", err))
		b.api.Send(msgErr)
		return
	}

	result := b.orch.Process(metricsData)
	
	msgText := fmt.Sprintf("🚀 *Token Analysis Completed*\n*Token:* `%s`\n*Result:* %+v\n*Liquidity:* $%.2f\n*Volume 5m:* %.2f", 
		metricsData.Token, result, metricsData.LiquidityUSD, metricsData.Volume5m)
	
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}
