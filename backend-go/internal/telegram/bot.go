package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/executor"
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
		case "status":
			b.handleStatus(update.Message.Chat.ID)
		case "positions":
			b.handlePositions(update.Message.Chat.ID)
		case "close":
			b.handleClose(update.Message.Chat.ID, args)
		case "closeall":
			b.handleCloseAll(update.Message.Chat.ID)
		case "dryrun":
			b.handleDryRun(update.Message.Chat.ID, args)
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
/analyze <tokenAddress> - Melakukan analisa instan pada sebuah koin (bypass scanner)
/config - Menampilkan konfigurasi bot (Risk Engine & Trade Settings)
/setconfig <key> <value> - Mengubah konfigurasi bot. Contoh: 
   - /setconfig minMcapSOL 500
   - /setconfig autoTrade false
/status - Cek status monitoring, wallet, balance, dan mode bot
/positions - Menampilkan posisi token yang sedang terbuka (open positions)
/close <index> - Menutup posisi (jual) secara manual berdasarkan indeks
/closeall - Menutup (menjual) semua posisi yang sedang terbuka
/dryrun on|off - Mengaktifkan atau menonaktifkan mode DRY RUN
   - /dryrun on  → Simulasi saja, tidak ada transaksi nyata
   - /dryrun off → LIVE TRADING, semua order akan dieksekusi di blockchain!

*⚠️ Mode DRY RUN:*
Saat DRY RUN aktif (default), bot akan mensimulasikan semua keputusan BUY/SELL tanpa transaksi nyata. Anda bisa mengamati bagaimana bot bekerja dengan aman. Untuk mulai trading nyata, gunakan /dryrun off.

*Cara Kerja Bot:*
1. *Screening Cycle:* Memindai koin baru di Solana via DexScreener sesuai interval (mirip Meridian).
2. *Risk Engine:* Menyaring token yang tidak sesuai batas di /config.
3. *AI LLM:* Menganalisa narasi dan metrik (dibantu oleh memori masa lalu / lessons.json).
4. *Executor:* Jika keputusan AI = BUY dan DRY RUN=off, bot otomatis menembak Jup.ag dan membeli sebesar 'maxDeployAmountSol'.
5. *Position Manager:* Otomatis memantau posisi terbuka dan menjual bila 'takeProfitPct' atau 'stopLossPct' tercapai, lalu mencatat hasilnya agar AI semakin cerdas (Self-Learning).

*Parameter Config Penting:*
- *autoTrade*: true/false (mengaktifkan auto beli)
- *dryRun*: true/false (mode simulasi, default=true)
- *scannerIntervalSec*: Interval waktu screening cycle (detik, disarankan 300 untuk mode santai)
- *minLiquiditySOL / maxLiquiditySOL*: Batas Likuiditas (dalam SOL)
- *minMcapSOL / maxMcapSOL*: Batas Market Cap (dalam SOL)
- *minVolumeSOL*: Volume minimal 5 menit terakhir (dalam SOL)
- *maxDeployAmountSol*: Modal maksimum tiap kali snipe (dalam SOL)
- *takeProfitPct / stopLossPct*: Persentase untung/rugi untuk menutup posisi (mis. TP: 20.0, SL: -10.0)
- *trailingTakeProfit*: true/false (aktifkan trailing TP)`

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	_, err := b.api.Send(msg)
	if err != nil {
		b.log.Error("Failed to send help message", zap.Error(err))
	}
}

func (b *Bot) handleHealth(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "✅ System is healthy and running smoothly.")
	b.api.Send(msg)
}

func (b *Bot) handleStatus(chatID int64) {
	cfg := b.mem.GetUserConfig()

	// ── Monitoring status ────────────────────────────────────────────────────
	statusText := "🔴 *Monitoring:* INACTIVE (autoTrade=false)"
	if cfg.AutoTrade {
		statusText = fmt.Sprintf("🟢 *Monitoring:* ACTIVE — scanning every *%ds*", cfg.ScannerIntervalSec)
	}

	// ── DRY RUN badge ────────────────────────────────────────────────────────
	modeBadge := "🔴 *Mode:* LIVE TRADING"
	if cfg.DryRun {
		modeBadge = "🧪 *Mode:* DRY RUN (simulasi, tidak ada transaksi nyata)"
	}

	// ── Wallet info ──────────────────────────────────────────────────────────
	walletInfo := "💳 *Wallet:* (gagal memuat — cek executor logs)"
	walletResp, err := executor.GetWalletBalance()
	if err == nil && walletResp != nil && walletResp.Success {
		walletInfo = fmt.Sprintf("💳 *Wallet:* `%s`\n💰 *Balance:* `%.4f SOL`",
			walletResp.Address, walletResp.BalanceSol)
	}

	// ── Positions summary ────────────────────────────────────────────────────
	positions := b.mem.GetPositions()
	openCount := 0
	totalCapital := 0.0
	for _, p := range positions {
		if !p.IsClosed {
			openCount++
			totalCapital += p.EntryAmount
		}
	}
	posSummary := fmt.Sprintf("📊 *Posisi Terbuka:* %d (total modal: %.4f SOL)", openCount, totalCapital)

	// ── Config summary ───────────────────────────────────────────────────────
	tpMode := "Fixed"
	if cfg.TrailingTakeProfit {
		tpMode = "Trailing"
	}
	configSummary := fmt.Sprintf(
		"⚙️ *Config:*\n"+
			"  • Snipe size: `%.4f SOL`\n"+
			"  • TP: `%.1f%%` (%s) | SL: `%.1f%%`\n"+
			"  • Min Liq: `%.0f SOL` | Max Liq: `%.0f SOL`\n"+
			"  • Min McAp: `%.0f SOL` | Max McAp: `%.0f SOL`\n"+
			"  • Min Vol5m: `%.1f SOL` | Organic≥: `%.0f`",
		cfg.MaxDeployAmountSol,
		cfg.TakeProfitPct, tpMode, cfg.StopLossPct,
		cfg.MinLiquiditySOL, cfg.MaxLiquiditySOL,
		cfg.MinMcapSOL, cfg.MaxMcapSOL,
		cfg.MinVolumeSOL, cfg.MinOrganicScore,
	)

	msgText := fmt.Sprintf("%s\n%s\n\n%s\n\n%s\n\n%s",
		statusText, modeBadge, walletInfo, posSummary, configSummary)
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handlePositions(chatID int64) {
	positions := b.mem.GetPositions()

	openPositions := []int{}
	for i, p := range positions {
		if !p.IsClosed {
			openPositions = append(openPositions, i)
		}
	}

	if len(openPositions) == 0 {
		b.api.Send(tgbotapi.NewMessage(chatID, "📂 *Tidak ada posisi terbuka saat ini.*"))
		return
	}

	msgText := fmt.Sprintf("📊 *Posisi Terbuka (%d):*\n\n", len(openPositions))
	for _, i := range openPositions {
		p := positions[i]
		age := time.Since(p.EntryTime).Round(time.Second)
		msgText += fmt.Sprintf("🔹 *[%d]* `%s`\n", i, p.TokenAddress)
		msgText += fmt.Sprintf("   💵 Masuk: `%.8f SOL` | Modal: `%.4f SOL`\n", p.EntryPrice, p.EntryAmount)
		msgText += fmt.Sprintf("   📈 Peak: `%.8f SOL`\n", p.HighestPrice)
		msgText += fmt.Sprintf("   ⏱ Umur: `%s`\n\n", age)
	}

	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handleAnalyze(chatID int64, args string) {
	token := strings.TrimSpace(args)
	if token == "" {
		msg := tgbotapi.NewMessage(chatID, "Gunakan: `/analyze <token_address>`")
		msg.ParseMode = "Markdown"
		b.api.Send(msg)
		return
	}

	msgWait := tgbotapi.NewMessage(chatID, fmt.Sprintf("⏳ Menganalisa token `%s`...", token))
	msgWait.ParseMode = "Markdown"
	b.api.Send(msgWait)

	metricsData, err := metrics.FetchTokenMetrics(token)
	if err != nil {
		msgErr := tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Error fetching metrics: %v", err))
		b.api.Send(msgErr)
		return
	}

	result := b.orch.Process(metricsData)

	// Format price change with emoji
	pc5m := metricsData.PriceChange5m
	pc5mStr := fmt.Sprintf("%.2f%%", pc5m)
	if pc5m > 0 {
		pc5mStr = "🟢 +" + pc5mStr
	} else if pc5m < -5 {
		pc5mStr = "🔴 " + pc5mStr
	} else {
		pc5mStr = "🟡 " + pc5mStr
	}

	ageStr := "N/A"
	if metricsData.PairAgeSec > 0 {
		ageStr = time.Duration(metricsData.PairAgeSec * int64(time.Second)).Round(time.Minute).String()
	}

	approvedStr := "REJECTED"
	if result.Approved {
		approvedStr = "APPROVED ✅"
	}

	msgText := fmt.Sprintf(
		"🔍 *Pipeline Analysis*\n"+
			"`%s`\n\n"+
			"💧 *Likuiditas:* `$%.0f` / `%.1f SOL`\n"+
			"📊 *Market Cap:* `$%.0f` / `%.0f SOL`\n"+
			"📈 *Vol 5m:* `$%.0f` | *Vol 1h:* `$%.0f`\n"+
			"💱 *Harga:* `%.8f SOL` ($%.8f)\n"+
			"📉 *Change 5m:* %s | *1h:* %.2f%%\n"+
			"🛒 *Buys/Sells (5m):* %d / %d | *BSR:* %.2fx\n"+
			"🧬 *Organic Score:* %.0f/100 | *Wash:* %.0f%%\n"+
			"⏱ *Pair Age:* %s\n\n"+
			"🤖 *Status:* `%s`\n"+
			"📊 *Confidence:* `%.0f%%`\n"+
			"🎯 *LLM:* `%s` (%.0f%%)\n"+
			"📏 *Size:* `%.4f SOL`\n"+
			"💪 *Momentum:* `%s` (%.2f)\n"+
			"🌍 *Regime:* `%s`\n"+
			"👤 *Deployer:* `%.2f` (%d rugs, %d tokens)\n"+
			"👥 *Holders:* `%d` (Top10: %.0f%%)\n"+
			"💧 *Liquidity:* `%s` (Change: %.1f%%)\n"+
			"🤖 *Cluster:* `%v` (%.0f%%)\n"+
			"🪐 *Jupiter Impact:* `%.2f%%`",
		metricsData.Token,
		metricsData.LiquidityUSD, metricsData.LiquiditySOL,
		metricsData.MarketCap, metricsData.MarketCapSOL,
		metricsData.Volume5m, metricsData.Volume1h,
		metricsData.PriceSOL, metricsData.PriceUSD,
		pc5mStr, metricsData.PriceChange1h,
		metricsData.Buys5m, metricsData.Sells5m, metricsData.BuySellRatio,
		metricsData.OrganicScore, metricsData.WashTradeProbability*100,
		ageStr,
		approvedStr,
		result.ConfidenceScore*100,
		result.LLMDecision, result.LLMConfidence*100,
		result.RecommendedSizeSOL,
		result.MomentumDirection, result.MomentumScore,
		result.MarketRegime,
		result.DeployerReputationScore, result.DeployerRugCount, result.DeployerTotalTokens,
		result.HolderCount, result.Top10HolderPct,
		result.LiquidityTrend, result.LiquidityChangeRate,
		result.WalletClusterDetected, result.ClusterBuyPct,
		result.JupiterPriceImpactPct,
	)

	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handleClose(chatID int64, args string) {
	idxStr := strings.TrimSpace(args)
	if idxStr == "" {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Penggunaan: `/close <index>`"))
		return
	}

	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Indeks harus berupa angka."))
		return
	}

	positions := b.mem.GetPositions()
	if idx < 0 || idx >= len(positions) {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Indeks tidak valid."))
		return
	}

	pos := positions[idx]
	if pos.IsClosed {
		b.api.Send(tgbotapi.NewMessage(chatID, "⚠️ Posisi tersebut sudah ditutup."))
		return
	}

	b.api.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("⏳ Sedang menutup posisi [%d] %s...", idx, pos.TokenAddress)))

	lamports := int64(pos.AmountToken * 1e6)
	resp, err := executor.ExecuteSwap(pos.TokenAddress, "So11111111111111111111111111111111111111112", lamports)
	if err != nil || (resp != nil && !resp.Success) {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Gagal menutup posisi. Cek log server."))
		return
	}

	positions[idx].IsClosed = true
	b.mem.SavePositions(positions)
	
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ *Posisi [%d] Berhasil Ditutup!*\nToken: `%s`", idx, pos.TokenAddress))
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handleCloseAll(chatID int64) {
	positions := b.mem.GetPositions()
	count := 0
	b.api.Send(tgbotapi.NewMessage(chatID, "⏳ Sedang memproses close all..."))

	for i, pos := range positions {
		if pos.IsClosed {
			continue
		}

		lamports := int64(pos.AmountToken * 1e6)
		resp, err := executor.ExecuteSwap(pos.TokenAddress, "So11111111111111111111111111111111111111112", lamports)
		if err == nil && resp != nil && resp.Success {
			positions[i].IsClosed = true
			count++
		}
	}

	if count > 0 {
		b.mem.SavePositions(positions)
	}

	b.api.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Selesai. %d posisi berhasil ditutup.", count)))
}

func (b *Bot) handleDryRun(chatID int64, args string) {
	arg := strings.TrimSpace(strings.ToLower(args))
	if arg != "on" && arg != "off" {
		cfg := b.mem.GetUserConfig()
		current := "OFF (LIVE TRADING 🔴)"
		if cfg.DryRun {
			current = "ON (Simulasi 🧪)"
		}
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
			"⚙️ *DRY RUN Mode saat ini:* %s\n\nGunakan:\n`/dryrun on` → aktifkan simulasi\n`/dryrun off` → aktifkan LIVE TRADING",
			current))
		msg.ParseMode = "Markdown"
		b.api.Send(msg)
		return
	}

	dryRun := arg == "on"
	if err := b.mem.UpdateUserConfig("dryRun", dryRun); err != nil {
		b.api.Send(tgbotapi.NewMessage(chatID, "❌ Gagal mengubah DRY RUN mode: "+err.Error()))
		return
	}

	var responseMsg string
	if dryRun {
		responseMsg = "🧪 *DRY RUN diaktifkan!*\n\nBot akan mensimulasikan semua keputusan BUY/SELL tanpa eksekusi nyata.\nAnda aman untuk mengamati bot bekerja."
	} else {
		responseMsg = "🔴 *LIVE TRADING diaktifkan!*\n\n⚠️ *PERINGATAN:* Semua order BUY/SELL AKAN dieksekusi di blockchain Solana menggunakan wallet Anda!\nPastikan Anda siap dan memiliki SOL yang cukup."
	}

	msg := tgbotapi.NewMessage(chatID, responseMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}
