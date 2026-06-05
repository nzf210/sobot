package telegram

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"btc-treasury/internal/config"
	"btc-treasury/internal/engine"
	"btc-treasury/internal/engine/engines"
	"btc-treasury/internal/exchange"
	"btc-treasury/internal/indicators"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/models"
	"btc-treasury/internal/monitor"
	"btc-treasury/internal/runtime"
	"btc-treasury/internal/scanner"
	"btc-treasury/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const helpText = `🤖 *BTC Treasury Accumulation*

*Account & Balances*
/btc_status — Spot balance \(USDT \+ all assets\), open orders
/btc_accounts — List configured bindings \(id/exchange\)
/btc_aggregate — Rollup of all bindings' BTC \+ PnL
/btc_use \<id\> \[exchange\] — Bind this chat to a specific binding

*Market & Analysis*
/btc_market \[PAIR\] — Live market data \+ OHLCV summary
/btc_advisory \[PAIR\] — Full quant \+ LLM advisory
/btc_scan \[PAIR\] — Scanner stats per pair \(AI scores\)

*Treasury & Positions*
/btc_treasury — BTC holdings, vault, compound balance, trade stats
/btc_positions — Open positions with TP/SL/trailing

*Pair Management \(BTC‑Quote\)*
/btc_pairs — List active scanned pairs
/btc_addpair \<PAIR\> — Add a single pair \(e\.g\. SOLBTC, ETHBTC, SUIBTC\)
/btc_addpairs \<PAIR1\> \<PAIR2\> … — Add multiple pairs in one command \(Binance only\)
/btc_removepair \<PAIR\> — Remove pair from scanner
/btc_discover — Auto\-discover all BTC\-quote pairs on the bound exchange
/btc_pairinfo \<PAIR\> — AI scores for one pair

*History & Learning*
/btc_history — Last 10 decisions
/btc_lessons — Recent self\-learning lessons

*Trading*
/btc_buy \<SIZE\> \<PAIR\> — Market buy with dynamic TP/SL
/btc_sell — Close ALL positions at market price
/btc_close \<index\> — Close position by index \(1\-based\)
/btc_closeall — Force close all positions
/btc_cancel — Cancel all open orders

*Bot Control*
/btc_dryrun on\|off — Toggle dry run mode \(simulation\)
/btc_pause — Pause trading \(24h\)
/btc_resume — Resume trading

*Configuration*
/btc_config — Current config \(TP/SL/thresholds\)
/btc_setconfig \<key\> \<value\> — Update config live
/btc_setcreds \<api\_key\> \<api\_secret\> \[passphrase\] — Update exchange API credentials live
/btc_enable — Enable LLM advisory
/btc_disable — Disable LLM advisory
/btc_report — Show report settings \(interval, enabled\)
/btc_setreport \<interval\_mins\> — Set report interval \(0=disabled\)

*Config Setup Guide*
\- Scanner Interval: /btc\_setconfig scanner\_interval \<seconds\> \(default: 900\=15min\)
\- Report Interval: /btc\_setreport \<minutes\> \(0=disabled, default: 5\)
\- LLM Threshold: /btc\_setconfig min\_score\_threshold \<0\-100\> \(default: 80\)
\- Risk/Trade: /btc\_setconfig risk\_per\_trade\_pct \<0\-100\>
\- TP: /btc\_setconfig take\_profit\_pct \<0\-100\>
\- SL: /btc\_setconfig stop\_loss\_pct \<negative\>
\- Trailing TP: /btc\_setconfig trailing\_tp\_pct \<0\-100\>
\- Compound %: /btc\_setconfig compound\_pct \<0\-100\>
\- BTC Vault %: /btc\_setconfig treasury\_pct \<0\-100\>

*Report Chat IDs \(ENV\)*
Set TELEGRAM\_REPORT\_CHAT\_IDS env var to receive periodic reports\.
Multiple IDs: TELEGRAM\_REPORT\_CHAT\_IDS=123,456,789

*Info*
/btc_skills — Full bot capabilities
/help — This message

*Pair Format \(BTC‑Quote\)*
Examples: SOLBTC, ETHBTC, SUIBTC, LINKBTC, DOGEBTC, ADABTC
Auto\-discover with /btc_discover

*Multi‑Exchange*
One account can run on Binance \\+ OKX simultaneously\\.
/btc_use main okx  → switch to OKX under the same id
/btc_status        → renders one block per exchange`

const skillsText = `*BTC Treasury Accumulation — Skills*

*1\. Binance Spot Scanner*
\\- Poll interval: every 15 min \(configurable\)
- Fetches OHLCV: 15m, 1h, 4h, 1d candles per BTC‑quote pair
- Auto\-discovers all BTC‑quote pairs from Binance
- Dynamic pair universe, no manual tracking needed

*2\. Relative Strength Engine*
- RS = Coin Return \- BTC Return
- Weight: 1h 35%, 4h 30%, 1d 25%, 15m 10%
- RS Rising = 1h RS \+ 4h RS → accelerating momentum

*3\. Momentum Engine*
- EMA20 \+ EMA50 \+ EMA200 alignment
- MACD bullish: MACD line \+ signal line \+ histogram
- RSI\(14\) ideal: 40\-60 continuation range
- Volume Growth: current \+ average comparison
- ATR expansion detection

*4\. Volume Engine*
- Volume Spike: current vol \+ 2x average
- Volume Expansion: 1h \+ 4h growing
- Wash Trade filter: wide spread \+ low move \+ high vol
- Liquidity check: reject thin pairs

*5\. AI Scoring Model*
\\| Component \\| Weight \\|
| Relative Strength | 40% |
| Volume Growth | 25% |
| Trend Strength | 20% |
| Volatility Quality | 10% |
| Market Structure | 5% |

Score \+ 80 → *AMBIL POSISI*
Score \* 80 → DO NOTHING \(cash is a position\)

*6\. Risk Manager*
- 1% risk per trade
- Max 1 position at a time
- 3 loss streak → Pause 24 hours
- Drawdown \+ 10% → Reduce position 50%
- Position size: risk_amount \+ SL distance

*7\. Entry Conditions \(ALL must pass\)*
✅ RS Rising \(1h RS \* 4h RS\)
✅ EMA20 \* EMA50 \* EMA200 bullish
✅ MACD bullish
✅ Volume \* Average
✅ AI Score \\\* 80

*8\. Exit Conditions*
- Take Profit: 3\-8% \(dynamic\)
- Trailing Stop: track peak, trigger on X% drop
- Stop Loss: 1\-2% \(hard limit\)
\\- TP \\\* \\|SL\\| always maintained

*9\. BTC Treasury Split*
On every winning close:
- 50% → BTC Treasury Vault \(never traded\)
- 50% → Compound balance \(re‑enter capital\)

*10\. Anti‑FOMO*
❌ Martingale
❌ Averaging Down
❌ Revenge Trading
❌ YOLO / All\-In

*Exchange: Binance Spot only — NO futures, NO perpetual, NO leverage*`

type BtcBot struct {
	token          string
	whitelist      []int64
	engine         *engine.AdvisoryEngine
	mem            memory.Store
	scanner        *scanner.ScannerState
	perAccount     map[exchange.AccountKey]*runtime.AccountRuntime
	activeAccount  map[int64]exchange.AccountKey
	activeAccLock  sync.RWMutex
	reportInterval uint64 // minutes, 0 = disabled
}

func NewBtcBot(
	token string,
	whitelist []int64,
	engine *engine.AdvisoryEngine,
	mem memory.Store,
	scanner *scanner.ScannerState,
	perAccount map[exchange.AccountKey]*runtime.AccountRuntime,
	reportInterval uint64,
) *BtcBot {
	return &BtcBot{
		token:          token,
		whitelist:      whitelist,
		engine:         engine,
		mem:            mem,
		scanner:        scanner,
		perAccount:     perAccount,
		activeAccount:  make(map[int64]exchange.AccountKey),
		reportInterval: reportInterval,
	}
}

func (b *BtcBot) resolveRuntime(chatID int64) *runtime.AccountRuntime {
	b.activeAccLock.RLock()
	defer b.activeAccLock.RUnlock()

	if key, ok := b.activeAccount[chatID]; ok {
		if rt, exists := b.perAccount[key]; exists {
			return rt
		}
	}

	for _, rt := range b.perAccount {
		return rt
	}
	return nil
}

func (b *BtcBot) resolveRuntimesForID(accountID string) []*runtime.AccountRuntime {
	var list []*runtime.AccountRuntime
	// Sort by exchange kind to maintain consistency
	var keys []exchange.AccountKey
	for k := range b.perAccount {
		if k.AccountID == accountID {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Exchange < keys[j].Exchange
	})

	for _, k := range keys {
		list = append(list, b.perAccount[k])
	}
	return list
}

func (b *BtcBot) isWhitelisted(chatID int64) bool {
	if len(b.whitelist) == 0 {
		log.Printf("TELEGRAM_WHITELIST_USER_BTC_IDS is empty — denying all trading commands.")
		return false
	}
	for _, id := range b.whitelist {
		if id == chatID {
			return true
		}
	}
	return false
}

func isPublicCommand(cmd string) bool {
	return cmd == "help" || cmd == "btcskills" || cmd == "start"
}

func (b *BtcBot) Start(ctx context.Context) {
	for {
		err := b.runBot(ctx)
		if err == nil {
			log.Printf("Telegram Bot: stopped cleanly")
			break
		}
		log.Printf("Telegram Bot: error encountered, restarting in 5s: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (b *BtcBot) runBot(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPI(b.token)
	if err != nil {
		return fmt.Errorf("failed to init bot: %w", err)
	}

	log.Printf("Telegram Bot: Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return nil
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			go b.handleMessage(ctx, bot, update.Message)
		}
	}
}

func (b *BtcBot) handleMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg.Text == "" {
		return
	}

	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}

	cmdPart := text[1:]
	var args string
	if idx := strings.Index(cmdPart, " "); idx != -1 {
		args = strings.TrimSpace(cmdPart[idx+1:])
		cmdPart = cmdPart[:idx]
	}

	cmd := strings.ToLower(cmdPart)
	cmd = strings.ReplaceAll(cmd, "_", "")

	chatID := msg.Chat.ID
	if !b.isWhitelisted(chatID) && !isPublicCommand(cmd) {
		reply := tgbotapi.NewMessage(chatID, "⛔ Unauthorized — set TELEGRAM_WHITELIST_USER_BTC_IDS to allow access")
		_, _ = bot.Send(reply)
		return
	}

	var err error
	switch cmd {
	case "help", "start":
		err = b.cmdHelp(bot, chatID)
	case "btcstatus":
		err = b.cmdStatus(ctx, bot, chatID)
	case "btcmarket":
		err = b.cmdMarket(ctx, bot, chatID, args)
	case "btcadvisory":
		err = b.cmdAdvisory(ctx, bot, chatID, args)
	case "btctreasury":
		err = b.cmdTreasury(bot, chatID)
	case "btcpositions":
		err = b.cmdPositions(bot, chatID)
	case "btcscan":
		err = b.cmdScan(ctx, bot, chatID, args)
	case "btchistory":
		err = b.cmdHistory(bot, chatID)
	case "btclessons":
		err = b.cmdLessons(bot, chatID)
	case "btcskills":
		err = b.cmdSkills(bot, chatID)
	case "btcpairs":
		err = b.cmdPairs(bot, chatID)
	case "btcaddpair":
		err = b.cmdAddPair(ctx, bot, chatID, args)
	case "btcaddpairs":
		err = b.cmdAddPairs(ctx, bot, chatID, args)
	case "btcremovepair":
		err = b.cmdRemovePair(bot, chatID, args)
	case "btcdiscover":
		err = b.cmdDiscover(ctx, bot, chatID)
	case "btcpairinfo":
		err = b.cmdPairInfo(ctx, bot, chatID, args)
	case "btcconfig":
		err = b.cmdConfig(bot, chatID)
	case "btcsetconfig":
		err = b.cmdSetConfig(bot, chatID, args)
	case "btcenable":
		err = b.cmdEnable(bot, chatID)
	case "btcdisable":
		err = b.cmdDisable(bot, chatID)
	case "btcbuy":
		err = b.cmdBuy(ctx, bot, chatID, args)
	case "btcsell":
		err = b.cmdSell(ctx, bot, chatID)
	case "btcclose":
		err = b.cmdClose(ctx, bot, chatID, args)
	case "btccloseall":
		err = b.cmdCloseAll(ctx, bot, chatID)
	case "btccancel":
		err = b.cmdCancel(ctx, bot, chatID)
	case "btcdryrun":
		err = b.cmdDryRun(bot, chatID, args)
	case "btcpause":
		err = b.cmdPause(bot, chatID)
	case "btcresume":
		err = b.cmdResume(bot, chatID)
	case "btcuse":
		err = b.cmdUse(bot, chatID, args)
	case "btcaccounts":
		err = b.cmdAccounts(ctx, bot, chatID)
	case "btcaggregate":
		err = b.cmdAggregate(bot, chatID)
	case "btcsetcreds":
		err = b.cmdSetCreds(ctx, bot, chatID, args)
	case "btcreport":
		err = b.cmdReport(bot, chatID)
	case "btcsetreport":
		err = b.cmdSetReport(bot, chatID, args)
	default:
		reply := tgbotapi.NewMessage(chatID, "Unknown command. Use /help")
		_, _ = bot.Send(reply)
	}

	if err != nil {
		log.Printf("Telegram Command Error (%s): %v", cmd, err)
		reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("Error: %v", err))
		_, _ = bot.Send(reply)
	}
}

func (b *BtcBot) cmdHelp(bot *tgbotapi.BotAPI, chatID int64) error {
	_, err := utils.SendMdv2Safe(bot, chatID, helpText)
	return err
}

func (b *BtcBot) cmdSkills(bot *tgbotapi.BotAPI, chatID int64) error {
	_, err := utils.SendMdv2Safe(bot, chatID, skillsText)
	return err
}

func (b *BtcBot) cmdStatus(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured. Set up accounts.json or EXCHANGE_API_KEY/EXCHANGE_API_SECRET.")
		return err
	}

	runtimes := b.resolveRuntimesForID(rt.AccountID)
	text, err := renderStatus(ctx, runtimes)
	if err != nil {
		return err
	}
	_, err = utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdMarket(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	pair := strings.TrimSpace(strings.ToUpper(args))
	if pair == "" {
		pair = "BTCUSDT"
	}

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	data, err := rt.Exchange.GetMarketData(ctx, pair)
	var text string
	if err != nil {
		text = fmt.Sprintf("Failed to fetch market data for %s: %v", pair, err)
	} else {
		text = fmt.Sprintf(
			"*%s — %s*\nRegime: %s\nTrend: %s\nVolume: %s/10\nLiquidity: %s/10\nSpread: %s/10\nVolatility: %s/10\nConfidence: %s",
			utils.EscapeMdv2(pair),
			utils.EscapeMdv2(rt.Exchange.ExchangeName()),
			utils.EscapeMdv2(data.MarketRegime),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", data.TrendStrength)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", data.VolumeScore)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", data.LiquidityScore)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", data.SpreadScore)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", data.VolatilityScore)),
			utils.EscapeMdv2(fmt.Sprintf("%.2f", data.Confidence)),
		)
	}
	_, err = utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdAdvisory(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	pair := strings.TrimSpace(strings.ToUpper(args))
	if pair == "" {
		pair = "BTCUSDT"
	}

	reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("🔍 Running advisory for %s...", pair))
	sentMsg, _ := bot.Send(reply)

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	marketData, err := rt.Exchange.GetMarketData(ctx, pair)
	if err != nil {
		marketData = models.BtcMarketData{Pair: pair}
	}
	orders, err := rt.Exchange.GetOpenOrders(ctx, pair)
	if err != nil {
		orders = []models.BtcAdvisoryPosition{}
	}

	treasury := rt.Mem.GetTreasuryState()
	input := models.BtcAdvisoryInput{
		MarketData:    marketData,
		Treasury:      treasury,
		OpenPositions: orders,
		LossStreak:    0,
	}

	advisory := rt.Engine.Analyze(ctx, &input)

	warningsStr := "none"
	if len(advisory.Warnings) > 0 {
		warningsStr = strings.Join(advisory.Warnings, ", ")
	}

	text := fmt.Sprintf(
		"*Advisory Result — %s*\n"+
			"Recommendation: *%s*\n"+
			"Confidence: %s\n"+
			"Risk Level: *%s*\n"+
			"Treasury Mode: %s\n"+
			"Market Regime: %s\n"+
			"LLM Active: %s\n\n"+
			"Reason: %s\n"+
			"Warnings: %s",
		utils.EscapeMdv2(pair),
		utils.EscapeMdv2(advisory.Recommendation),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", advisory.Confidence)),
		utils.EscapeMdv2(advisory.RiskLevel),
		utils.EscapeMdv2(advisory.TreasuryMode),
		utils.EscapeMdv2(advisory.MarketRegime),
		utils.EscapeMdv2(fmt.Sprintf("%t", advisory.BypassQuant)),
		utils.EscapeMdv2(advisory.Reason),
		utils.EscapeMdv2(warningsStr),
	)

	_, err = utils.SendMdv2Safe(bot, chatID, text)
	if sentMsg.MessageID != 0 {
		delMsg := tgbotapi.NewDeleteMessage(chatID, sentMsg.MessageID)
		_, _ = bot.Request(delMsg)
	}
	return err
}

func (b *BtcBot) cmdTreasury(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	ts := rt.Mem.GetTreasuryState()
	cfg := rt.Mem.GetConfig()

	text := fmt.Sprintf(
		"🏦 *BTC Treasury — %s*\n"+
			"BTC Holdings: %s\n"+
			"BTC Vault: %s\n"+
			"Compound: %s\n"+
			"Stable Value: %s\n"+
			"USDT Balance: %s\n"+
			"Total Trades: %s\n"+
			"Winning Trades: %s \\(win %s%%\\)\n"+
			"Losing Trades: %s\n"+
			"Consecutive Losses: %s\n"+
			"Growth 7d: %s%%\n"+
			"Growth 30d: %s%%\n"+
			"Last Update: %s\n"+
			"Mode: %s\n"+
			"LLM Advisory: %s",
		utils.EscapeMdv2(rt.Exchange.ExchangeName()),
		utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CurrentBtc)),
		utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.BtcTreasuryVault)),
		utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CompoundBalance)),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.StableValue)),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.UsdtBalance)),
		utils.EscapeMdv2(fmt.Sprintf("%d", ts.TotalTrades)),
		utils.EscapeMdv2(fmt.Sprintf("%d", ts.WinningTrades)),
		utils.EscapeMdv2(fmt.Sprintf("%.0f", func() float64 {
			if ts.TotalTrades > 0 {
				return float64(ts.WinningTrades) / float64(ts.TotalTrades) * 100.0
			}
			return 0
		}())),
		utils.EscapeMdv2(fmt.Sprintf("%d", ts.LosingTrades)),
		utils.EscapeMdv2(fmt.Sprintf("%d", ts.ConsecutiveLosses)),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.BtcGrowth7d*100.0)),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.BtcGrowth30d*100.0)),
		utils.EscapeMdv2(ts.LastUpdate),
		func() string {
			if cfg.DryRun {
				return "🧪 DRY RUN"
			}
			return "🔴 LIVE"
		}(),
		func() string {
			if cfg.LLMEnabled {
				return "✅ ON"
			}
			return "❌ OFF"
		}(),
	)

	_, err := utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdPositions(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	positions := rt.Mem.GetPositions()
	if len(positions) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No open positions")
		return err
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("📂 *Open Positions \\(%s\\)*", utils.EscapeMdv2(fmt.Sprintf("%d", len(positions)))))
	for i, p := range positions {
		lines = append(lines, fmt.Sprintf(
			"%s\\. *%s*\n"+
				"  Side: %s\n"+
				"  Size: %s\n"+
				"  Entry Price: %s\n"+
				"  Current Price: %s\n"+
				"  PnL BTC: %s\n"+
				"  TP: %s%% \\(SL: %s%%\\)\n"+
				"  Entry Time: %s",
			utils.EscapeMdv2(fmt.Sprintf("%d", i+1)),
			utils.EscapeMdv2(p.ID),
			utils.EscapeMdv2(p.Side),
			utils.EscapeMdv2(fmt.Sprintf("%.6f", p.Size)),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", p.EntryPrice)),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", p.CurrentPrice)),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", p.PnlBtc)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", p.TakeProfitPct)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", p.StopLossPct)),
			utils.EscapeMdv2(p.EntryTime),
		))
	}

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n\n"))
	return err
}

func (b *BtcBot) cmdScan(_ context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Scanner not active")
		return err
	}
	scannerState := rt.ScannerState

	pair := strings.TrimSpace(strings.ToUpper(args))
	if pair != "" {
		ps := scannerState.GetPairState(pair)
		if ps == nil {
			reply := tgbotapi.NewMessage(chatID, fmt.Sprintf("Pair '%s' not found in scanner", pair))
			_, err := bot.Send(reply)
			return err
		}

		snapshot := ps.Stats.Snapshot()
		lastTime := ps.GetLastScanTime()
		lastRegime := ps.GetLastRegime()
		lastRec := ps.GetLastRecommendation()
		lastConf := ps.GetLastConfidence()
		lastRisk := ps.GetLastRiskLevel()
		lastReason := ps.GetLastReason()

		timeShort := lastTime
		if len(lastTime) > 16 {
			timeShort = lastTime[11:19]
		} else if lastTime == "" {
			timeShort = "never"
		}

		reasonShort := lastReason
		if len(lastReason) > 80 {
			reasonShort = lastReason[:77] + "..."
		}

		bar := scoreBar(lastConf)

		text := fmt.Sprintf(
			"*%s — Scanner*\n\n"+
				"Scans: %s \\| ✅ %s \\| 👁 %s \\| 🛡 %s \\| ❌ %s \\| ⚠️ %s\n\n"+
				"Last Scan: %s\n"+
				"Regime: %s\n"+
				"Recommendation: *%s*\n"+
				"AI Score: %s%s\n"+
				"Risk: %s\n"+
				"%s",
			utils.EscapeMdv2(pair),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Scanned)),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Approve)),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Monitor)),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Protect)),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Reject)),
			utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Errors)),
			utils.EscapeMdv2(timeShort),
			utils.EscapeMdv2(lastRegime),
			utils.EscapeMdv2(lastRec),
			bar,
			utils.EscapeMdv2(fmt.Sprintf("%.2f", lastConf)),
			utils.EscapeMdv2(lastRisk),
			utils.EscapeMdv2(reasonShort),
		)
		_, err := utils.SendMdv2Safe(bot, chatID, text)
		return err
	}

	snapshots := scannerState.AllSnapshots()
	if len(snapshots) == 0 {
		reply := tgbotapi.NewMessage(chatID, "No pairs configured\nUse /btc_addpair or /btc_discover to add pairs")
		_, err := bot.Send(reply)
		return err
	}

	cfg := rt.Mem.GetConfig()
	lines := []string{fmt.Sprintf("*Scanner — AI Scores \\(threshold: %s\\)*\n", utils.EscapeMdv2(fmt.Sprintf("%.0f", cfg.MinScoreThreshold)))}
	for _, s := range snapshots {
		var icon string
		switch s.LastRecommendation {
		case "APPROVE":
			icon = "✅"
		case "MONITOR":
			icon = "👁"
		case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
			icon = "🛡"
		default:
			if s.LastRecommendation == "" {
				icon = "⏳"
			} else {
				icon = "❌"
			}
		}

		bar := scoreBar(s.LastConfidence)
		lines = append(lines, fmt.Sprintf(
			"%s %s — AI: %s%s \\| %s \\| %s",
			utils.EscapeMdv2(icon),
			utils.EscapeMdv2(s.Pair),
			bar,
			utils.EscapeMdv2(fmt.Sprintf("%.2f", s.LastConfidence)),
			utils.EscapeMdv2(s.LastRecommendation),
			utils.EscapeMdv2(s.LastRiskLevel),
		))
	}

	lines = append(lines, "", "ℹ️ Score ≥ 80 \\= AMBIL POSISI \\| < 80 \\= DO NOTHING")
	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdPairs(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	pairs := rt.ScannerState.GetPairs()
	if len(pairs) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No pairs configured in scanner")
		return err
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("🔍 *Scanned Pairs \\(%s\\)*\n", utils.EscapeMdv2(fmt.Sprintf("%d", len(pairs)))))
	for i, p := range pairs {
		lines = append(lines, fmt.Sprintf("%s\\. `%s`", utils.EscapeMdv2(fmt.Sprintf("%d", i+1)), utils.EscapeMdv2(p)))
	}

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdAddPair(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	pair, err := validatePairToken(args)
	if err != nil {
		reply := fmt.Sprintf(
			"Usage: /btc_addpair <PAIR>\n\n%s\n\nExamples:\n  /btc_addpair SOLBTC\n  /btc_addpair ETHBTC\n  /btc_addpair SUIBTC\n\nOr use /btc_addpairs to add several at once, or /btc_discover for the full universe.",
			err.Error(),
		)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Scanner not active (exchange not configured)")
		return err
	}

	valid, err := rt.Exchange.ValidateSymbol(ctx, pair)
	if err != nil {
		reply := fmt.Sprintf("Failed to verify '%s': %v", pair, err)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}
	if !valid {
		reply := fmt.Sprintf("Pair '%s' not found on %s or not trading", pair, rt.Exchange.ExchangeName())
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	if rt.ScannerState.AddPair(pair) {
		pairs := rt.ScannerState.GetPairs()
		cfg := rt.Mem.GetConfig()
		cfg.ScannerPairs = pairs
		rt.Mem.SaveConfig(cfg)
		reply := fmt.Sprintf("✅ Added '%s' to scanner\n%d pairs now active: %s", pair, len(pairs), strings.Join(pairs, ", "))
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	reply := fmt.Sprintf("'%s' already in scanner", pair)
	_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
	return err
}

func (b *BtcBot) cmdAddPairs(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	raw := strings.TrimSpace(args)
	if raw == "" {
		reply := "Usage: /btc_addpairs <PAIR1> <PAIR2> [PAIR3 ...]\nExamples:\n  /btc_addpairs SOLBTC ETHBTC\n  /btc_addpairs SOLBTC, ETHBTC, SUIBTC\n\nOr use /btc_addpair for a single pair."
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	var tokens []string
	for _, rawTok := range strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' }) {
		tok := strings.TrimSpace(rawTok)
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}

	if len(tokens) == 0 {
		reply := "Usage: /btc_addpairs <PAIR1> <PAIR2> [PAIR3 ...]"
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	if len(tokens) == 1 {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Tip: for a single pair, use /btc_addpair <PAIR>"))
	}

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Scanner not active (exchange not configured)")
		return err
	}

	validPairs, err := rt.Exchange.DiscoverBtcPairs(ctx)
	if err != nil {
		reply := fmt.Sprintf("Multi-pair discovery not supported on %s (%v). Add pairs one at a time with /btc_addpair <PAIR>.", rt.Exchange.ExchangeName(), err)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	validSet := make(map[string]bool)
	for _, p := range validPairs {
		validSet[strings.ToUpper(p)] = true
	}

	if len(validSet) == 0 {
		reply := fmt.Sprintf("Live pair list came back empty from %s. Try again in a few seconds.", rt.Exchange.ExchangeName())
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	var lines []string
	var added, dupes, invalid, notFound int

	for _, token := range tokens {
		pair, err := validatePairToken(token)
		if err != nil {
			lines = append(lines, fmt.Sprintf("❌ %s — %v", token, err))
			invalid++
			continue
		}
		if !validSet[pair] {
			lines = append(lines, fmt.Sprintf("❌ %s — not found on %s", pair, rt.Exchange.ExchangeName()))
			notFound++
			continue
		}
		if rt.ScannerState.AddPair(pair) {
			lines = append(lines, fmt.Sprintf("✅ %s — added", pair))
			added++
		} else {
			lines = append(lines, fmt.Sprintf("⚠️ %s — already in scanner", pair))
			dupes++
		}
	}

	if added > 0 {
		pairs := rt.ScannerState.GetPairs()
		cfg := rt.Mem.GetConfig()
		cfg.ScannerPairs = pairs
		rt.Mem.SaveConfig(cfg)
	}

	text := fmt.Sprintf(
		"*Add Multiple Pairs — [%s]*\n\n%s\n\n*Summary:* %s added, %s duplicates, %s not found, %s invalid",
		utils.EscapeMdv2(rt.Exchange.ExchangeName()),
		strings.Join(lines, "\n"),
		utils.EscapeMdv2(fmt.Sprintf("%d", added)),
		utils.EscapeMdv2(fmt.Sprintf("%d", dupes)),
		utils.EscapeMdv2(fmt.Sprintf("%d", notFound)),
		utils.EscapeMdv2(fmt.Sprintf("%d", invalid)),
	)
	_, err = utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdRemovePair(bot *tgbotapi.BotAPI, chatID int64, args string) error {
	pair := strings.TrimSpace(strings.ToUpper(args))
	if pair == "" {
		reply := "Usage: /btc_removepair <PAIR>\nExample: /btc_removepair DOGEBTC"
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Scanner not active")
		return err
	}

	if rt.ScannerState.RemovePair(pair) {
		pairs := rt.ScannerState.GetPairs()
		cfg := rt.Mem.GetConfig()
		cfg.ScannerPairs = pairs
		rt.Mem.SaveConfig(cfg)
		reply := fmt.Sprintf("✅ Removed '%s'\n%d pairs remaining: %s", pair, len(pairs), strings.Join(pairs, ", "))
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	reply := fmt.Sprintf("'%s' not found in scanner", pair)
	_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
	return err
}

func (b *BtcBot) cmdDiscover(_ context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("🔍 Discovering BTC-quote pairs on %s...", rt.Exchange.ExchangeName())))

	if rt.Exchange.ExchangeName() != "Binance" {
		_, err := bot.Send(tgbotapi.NewMessage(chatID, "Auto-discover only works with Binance"))
		return err
	}

	commonPairs := []string{
		"ETHBTC", "SOLBTC", "SUIBTC", "LINKBTC", "DOGEBTC",
		"ADABTC", "XRPBTC", "AVAXBTC", "DOTBTC", "MATICBTC",
		"LTCBTC", "UNIBTC", "AAVEBTC", "ATOMBTC", "FETBTC",
		"NEARBTC", "FTMBTC", "ALGOBTC", "ICPBTC", "ARBBTC",
	}

	var lines []string
	for _, p := range commonPairs {
		lines = append(lines, fmt.Sprintf("  • %s", p))
	}

	text := fmt.Sprintf(
		"*Auto-discover BTC-Quote Pairs*\n\n"+
			"%s has ~50 BTC-quote pairs.\n"+
			"Use /btc_addpair to add them one by one:\n\n"+
			"/btc_addpair ETHBTC\n"+
			"/btc_addpair SOLBTC\n"+
			"/btc_addpair SUIBTC\n"+
			"...etc\n\n"+
			"Popular pairs:\n%s",
		utils.EscapeMdv2(rt.Exchange.ExchangeName()),
		strings.Join(lines, "\n"),
	)
	_, err := utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdPairInfo(_ context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	pair := strings.TrimSpace(strings.ToUpper(args))
	if pair == "" {
		reply := "Usage: /btc_pairinfo <PAIR>\nExample: /btc_pairinfo SOLBTC"
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Scanner not active")
		return err
	}

	ps := rt.ScannerState.GetPairState(pair)
	if ps == nil {
		reply := fmt.Sprintf("Pair '%s' not found. Add it with /btc_addpair %s", pair, pair)
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	snapshot := ps.Stats.Snapshot()
	lastTime := ps.GetLastScanTime()
	lastRegime := ps.GetLastRegime()
	lastRec := ps.GetLastRecommendation()
	lastConf := ps.GetLastConfidence()
	lastRisk := ps.GetLastRiskLevel()
	lastReason := ps.GetLastReason()
	bar := scoreBar(lastConf)
	cfg := rt.Mem.GetConfig()

	timeShort := lastTime
	if len(lastTime) > 16 {
		timeShort = lastTime[11:19]
	}

	text := fmt.Sprintf(
		"*%s — AI Scores*\n\n"+
			"*Overall:* %s%s / 100\n"+
			"Threshold: %s\n"+
			"Decision: *%s*\n\n"+
			"*Scanner Stats*\n"+
			"Total Scans: %s\n"+
			"✅ Approve: %s \\| 👁 Monitor: %s \\| 🛡 Protect: %s \\| ❌ Reject: %s\n\n"+
			"*Last Scan \\(%s\\)*\n"+
			"Regime: %s\n"+
			"Risk: %s\n"+
			"Reason: %s",
		utils.EscapeMdv2(pair),
		bar,
		utils.EscapeMdv2(fmt.Sprintf("%.2f", lastConf)),
		utils.EscapeMdv2(fmt.Sprintf("%.0f", cfg.MinScoreThreshold)),
		utils.EscapeMdv2(lastRec),
		utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Scanned)),
		utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Approve)),
		utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Monitor)),
		utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Protect)),
		utils.EscapeMdv2(fmt.Sprintf("%d", snapshot.Reject)),
		utils.EscapeMdv2(timeShort),
		utils.EscapeMdv2(lastRegime),
		utils.EscapeMdv2(lastRisk),
		utils.EscapeMdv2(lastReason),
	)
	_, err := utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdHistory(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	decisions := rt.Mem.GetDecisions()
	if len(decisions) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No decision history found")
		return err
	}

	var lines []string
	lines = append(lines, "📜 *Recent Decisions (Last 10)*\n")

	count := 0
	for i := len(decisions) - 1; i >= 0 && count < 10; i-- {
		d := decisions[i]
		icon := "❌"
		switch d.Advisory.Recommendation {
		case "APPROVE":
			icon = "✅"
		case "MONITOR":
			icon = "👁"
		case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
			icon = "🛡"
		}

		timeShort := d.Timestamp
		if len(d.Timestamp) > 16 {
			timeShort = d.Timestamp[11:19]
		}

		lines = append(lines, fmt.Sprintf(
			"%s\\. `%s` *%s* %s %s \\(conf: %.2f, score: %s\\)\n  \\_`%s`",
			utils.EscapeMdv2(fmt.Sprintf("%d", count+1)),
			utils.EscapeMdv2(timeShort),
			utils.EscapeMdv2(d.MarketData.Pair),
			utils.EscapeMdv2(icon),
			utils.EscapeMdv2(d.Advisory.Recommendation),
			d.Advisory.Confidence,
			utils.EscapeMdv2(fmt.Sprintf("%.0f", d.Advisory.OpportunityScore)),
			utils.EscapeMdv2(d.Advisory.Reason),
		))
		count++
	}

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdLessons(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	lessons := rt.Mem.GetLessons()
	if len(lessons) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No lessons logged yet")
		return err
	}

	var lines []string
	lines = append(lines, "📚 *Self-Learning Lessons (Last 10)*\n")

	count := 0
	for i := len(lessons) - 1; i >= 0 && count < 10; i-- {
		lesson := lessons[i]
		short := lesson
		if len(lesson) > 150 {
			short = lesson[:147] + "..."
		}
		lines = append(lines, fmt.Sprintf("%s\\. %s", utils.EscapeMdv2(fmt.Sprintf("%d", count+1)), utils.EscapeMdv2(short)))
		count++
	}

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdConfig(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	cfg := rt.Mem.GetConfig()
	ts := rt.Mem.GetTreasuryState()

	var winRate float64
	if ts.TotalTrades > 0 {
		winRate = float64(ts.WinningTrades) / float64(ts.TotalTrades) * 100.0
	}

	text := fmt.Sprintf(
		"⚙️ *Config — BTC Treasury Accumulation [%s / %s]*\n\n"+
			"*Trading*\n"+
			"Exchange: %s\n"+
			"Mode: %s\n"+
			"Initial Capital: ${%s}\n"+
			"Max Positions: %s\n"+
			"Risk/Trade: %.1f%%\n\n"+
			"*Thresholds*\n"+
			"AI Score Threshold: %s \\(>= 80 = AMBIL POSISI\\)\n"+
			"Min Confidence: %.2f\n"+
			"Max Exposure: %.2f\n\n"+
			"*Entry/Exit*\n"+
			"Take Profit: %.1f%%\n"+
			"Stop Loss: %.1f%%\n"+
			"Trailing TP: %.1f%% — %s\n\n"+
			"*Treasury Split*\n"+
			"Compound: %.0f%%\n"+
			"BTC Vault: %.0f%%\n\n"+
			"*Risk Controls*\n"+
			"Max Consecutive Losses: %s\n"+
			"Daily Loss Limit: %.8f BTC\n"+
			"Pause on Drawdown > 10%%\n\n"+
			"*Scanner*\n"+
			"Pairs: %s\n"+
			"Win Rate: %s%%\n"+
			"Paused Until: %s",
		utils.EscapeMdv2(rt.AccountID),
		utils.EscapeMdv2(string(rt.Spec.Exchange)),
		utils.EscapeMdv2(rt.Exchange.ExchangeName()),
		func() string {
			if cfg.DryRun {
				return "🧪 DRY RUN"
			}
			return "🔴 LIVE"
		}(),
		utils.EscapeMdv2(fmt.Sprintf("%.2f", cfg.InitialCapitalUsdt)),
		utils.EscapeMdv2(fmt.Sprintf("%d", cfg.MaxPositions)),
		cfg.RiskPerTradePct*100.0,
		utils.EscapeMdv2(fmt.Sprintf("%.0f", cfg.MinScoreThreshold)),
		cfg.MinConfidence,
		cfg.MaxExposure,
		cfg.TakeProfitPct,
		cfg.StopLossPct,
		cfg.TrailingTpPct,
		func() string {
			if cfg.UseTrailing {
				return "ON"
			}
			return "OFF"
		}(),
		cfg.CompoundPct*100.0,
		cfg.TreasuryPct*100.0,
		utils.EscapeMdv2(fmt.Sprintf("%d", cfg.MaxConsecutiveLosses)),
		cfg.DailyLossLimitBtc,
		utils.EscapeMdv2(fmt.Sprintf("%d", len(cfg.ScannerPairs))),
		utils.EscapeMdv2(fmt.Sprintf("%.1f", winRate)),
		func() string {
			if ts.TradingPausedUntil == "" {
				return "—"
			}
			return utils.EscapeMdv2(ts.TradingPausedUntil)
		}(),
	)

	_, err := utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdSetConfig(bot *tgbotapi.BotAPI, chatID int64, args string) error {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) != 2 {
		_, err := utils.SendMdv2Safe(bot, chatID, "Usage: /btc_setconfig <key> <value>")
		return err
	}
	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])

	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	cfg := rt.Mem.GetConfig()
	updated := false
	var newValStr string

	switch key {
	case "enabled":
		v, err := strconv.ParseBool(val)
		if err == nil {
			cfg.Enabled = v
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid boolean: %s", val)))
			return nil
		}
	case "take_profit_pct":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 && v <= 100.0 {
			cfg.TakeProfitPct = v
			updated = true
			newValStr = fmt.Sprintf("%.1f%%", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be 0-100)", val)))
			return nil
		}
	case "stop_loss_pct":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v <= 0.0 && v >= -100.0 {
			cfg.StopLossPct = v
			updated = true
			newValStr = fmt.Sprintf("%.1f%%", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be negative)", val)))
			return nil
		}
	case "trailing_tp_pct":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 {
			cfg.TrailingTpPct = v
			updated = true
			newValStr = fmt.Sprintf("%.1f%%", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be positive)", val)))
			return nil
		}
	case "use_trailing":
		v, err := strconv.ParseBool(val)
		if err == nil {
			cfg.UseTrailing = v
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid boolean: %s", val)))
			return nil
		}
	case "min_score_threshold":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 && v <= 100.0 {
			cfg.MinScoreThreshold = v
			updated = true
			newValStr = fmt.Sprintf("%.0f", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be 0-100)", val)))
			return nil
		}
	case "risk_per_trade_pct":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 && v <= 100.0 {
			cfg.RiskPerTradePct = v / 100.0
			updated = true
			newValStr = fmt.Sprintf("%.1f%%", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be 0-100)", val)))
			return nil
		}
	case "max_positions":
		v, err := strconv.ParseInt(val, 10, 32)
		if err == nil && v >= 0 && v <= 10 {
			cfg.MaxPositions = int(v)
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be 0-10)", val)))
			return nil
		}
	case "compound_pct":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 && v <= 100.0 {
			cfg.CompoundPct = v / 100.0
			updated = true
			newValStr = fmt.Sprintf("%.0f%%", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be 0-100)", val)))
			return nil
		}
	case "initial_capital_usdt":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v > 0.0 {
			cfg.InitialCapitalUsdt = v
			updated = true
			newValStr = fmt.Sprintf("$%.2f", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be > 0)", val)))
			return nil
		}
	case "dry_run":
		v, err := strconv.ParseBool(val)
		if err == nil {
			cfg.DryRun = v
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid boolean: %s", val)))
			return nil
		}
	case "llm_activation_threshold", "min_confidence", "max_exposure", "safe_mode_volatility", "safe_mode_drawdown":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil {
			if v < 0.0 {
				v = 0.0
			} else if v > 1.0 {
				v = 1.0
			}
			switch key {
			case "llm_activation_threshold":
				cfg.LlmActivationThreshold = v
			case "min_confidence":
				cfg.MinConfidence = v
			case "max_exposure":
				cfg.MaxExposure = v
			case "safe_mode_volatility":
				cfg.SafeModeVolatility = v
			case "safe_mode_drawdown":
				cfg.SafeModeDrawdown = v
			}
			updated = true
			newValStr = fmt.Sprintf("%.4f", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s", val)))
			return nil
		}
	case "llm_enabled":
		v, err := strconv.ParseBool(val)
		if err == nil {
			cfg.LLMEnabled = v
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid boolean: %s", val)))
			return nil
		}
	case "daily_loss_limit_btc":
		v, err := strconv.ParseFloat(val, 64)
		if err == nil && v >= 0.0 {
			cfg.DailyLossLimitBtc = v
			updated = true
			newValStr = fmt.Sprintf("%.8f BTC", v)
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be >= 0)", val)))
			return nil
		}
	case "max_consecutive_losses":
		v, err := strconv.ParseInt(val, 10, 32)
		if err == nil && v >= 0 {
			cfg.MaxConsecutiveLosses = int(v)
			updated = true
			newValStr = val
		} else {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Invalid value: %s (must be >= 0)", val)))
			return nil
		}
	default:
		reply := "Available keys:\n  take_profit_pct, stop_loss_pct, trailing_tp_pct, use_trailing\n  min_score_threshold, risk_per_trade_pct, max_positions\n  compound_pct, initial_capital_usdt, dry_run\n  enabled, llm_enabled, llm_activation_threshold, min_confidence, max_exposure\n  max_consecutive_losses, daily_loss_limit_btc\n\nExample: /btc_setconfig llm_enabled false\nExample: /btc_setconfig take_profit_pct 6.0"
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return nil
	}

	if updated {
		rt.Mem.SaveConfig(cfg)
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ %s = %s", key, newValStr)))
	}
	return nil
}

func (b *BtcBot) cmdEnable(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	cfg := rt.Mem.GetConfig()
	cfg.Enabled = true
	rt.Mem.SaveConfig(cfg)
	_, err := utils.SendMdv2Safe(bot, chatID, "✅ LLM advisory *ENABLED*")
	return err
}

func (b *BtcBot) cmdDisable(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	cfg := rt.Mem.GetConfig()
	cfg.Enabled = false
	rt.Mem.SaveConfig(cfg)
	_, err := utils.SendMdv2Safe(bot, chatID, "⏸️ LLM advisory *DISABLED*")
	return err
}

func (b *BtcBot) cmdCancel(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	pairs := rt.ScannerState.GetPairs()
	var total int
	for _, pair := range pairs {
		res, err := rt.Exchange.CancelAll(ctx, pair)
		if err == nil {
			total += len(res)
		}
	}

	var reply string
	if len(pairs) == 0 {
		res, err := rt.Exchange.CancelAll(ctx, "BTCUSDT")
		if err == nil {
			reply = fmt.Sprintf("✅ Cancelled %d open orders", len(res))
		} else {
			reply = fmt.Sprintf("Failed: %v", err)
		}
	} else {
		reply = fmt.Sprintf("✅ Cancelled %d open orders across %d pairs", total, len(pairs))
	}

	_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
	return err
}

func (b *BtcBot) cmdBuy(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		reply := "Usage: /btc_buy <SIZE> <PAIR>\nExamples:\n  /btc_buy 100 SOLBTC\n  /btc_buy 0.5 ETHBTC\n  /btc_buy 10 BTCUSDT"
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	sizeStr := parts[0]
	pair := "BTCUSDT"
	if len(parts) >= 2 {
		pair = strings.ToUpper(parts[1])
	}

	size, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil || size <= 0.0 {
		reply := fmt.Sprintf("Invalid size: '%s'. Must be a positive number.", sizeStr)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("📈 Placing BUY order on %s...\n%.6f %s @ market price...", rt.Exchange.ExchangeName(), size, pair)))

	cfg := rt.Mem.GetConfig()
	ts := rt.Mem.GetTreasuryState()

	if ts.TradingPausedUntil != "" {
		if paused, err := time.Parse(time.RFC3339, ts.TradingPausedUntil); err == nil {
			if time.Now().UTC().Before(paused.UTC()) {
				reply := fmt.Sprintf("⏸️ Trading is PAUSED until %s\nUse /btc_resume to resume.", paused.Format("2006-01-02 15:04 UTC"))
				_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
				return err
			}
		}
	}

	if cfg.DryRun {
		marketData, err := rt.Exchange.GetMarketData(ctx, pair)
		if err != nil {
			marketData = models.BtcMarketData{Pair: pair}
		}
		advisory := rt.Engine.Analyze(ctx, &models.BtcAdvisoryInput{
			MarketData:    marketData,
			Treasury:      ts,
			OpenPositions: rt.Mem.GetPositions(),
			LossStreak:    0,
		})
		currentPrice, _ := rt.Exchange.GetCurrentPrice(ctx, pair)
		baseSize := size
		if currentPrice > 0.0 {
			baseSize = size / currentPrice
		}
		monitor.RecordPositionFromAdvisory(rt.Mem, &advisory, currentPrice, baseSize, pair, "buy")

		reply := fmt.Sprintf("🧪 *DRY RUN — Simulated Buy*\nPair: %s\nSize: %s\nTP: %s%% \\| SL: %s%%\nReason: %s",
			utils.EscapeMdv2(pair),
			utils.EscapeMdv2(fmt.Sprintf("%.6f", size)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", advisory.DynamicTakeProfit)),
			utils.EscapeMdv2(fmt.Sprintf("%.1f", advisory.DynamicStopLoss)),
			utils.EscapeMdv2(advisory.TpReason))
		_, err = utils.SendMdv2Safe(bot, chatID, reply)
		return err
	}

	marketData, err := rt.Exchange.GetMarketData(ctx, pair)
	if err != nil {
		reply := fmt.Sprintf("Failed to fetch market data: %v", err)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	positions := rt.Mem.GetPositions()
	var streak int
	for i := len(positions) - 1; i >= 0; i-- {
		if positions[i].PnlBtc < 0.0 {
			streak++
		} else {
			break
		}
	}

	input := models.BtcAdvisoryInput{
		MarketData:    marketData,
		Treasury:      ts,
		OpenPositions: positions,
		LossStreak:    streak,
	}

	advisory := rt.Engine.Analyze(ctx, &input)
	currentPrice, _ := rt.Exchange.GetCurrentPrice(ctx, pair)

	if currentPrice > 0.0 {
		candles, err := rt.Exchange.GetKlines(ctx, pair, "15m", 200)
		if err == nil {
			atr14 := indicators.ATR(candles, 14)
			riskManager := engines.RiskManager{}
			clampedSL := riskManager.ClampSl(advisory.DynamicStopLoss, currentPrice, atr14)
			if clampedSL != advisory.DynamicStopLoss {
				tpSlRatio := 3.0
				if advisory.DynamicStopLoss != 0.0 {
					tpSlRatio = math.Max(advisory.DynamicTakeProfit/math.Abs(advisory.DynamicStopLoss), 2.0)
				}
				advisory.DynamicStopLoss = clampedSL
				advisory.DynamicTakeProfit = math.Abs(clampedSL) * tpSlRatio
				advisory.SlReason = fmt.Sprintf("%s (ATR clamp: %.1f%% min)", advisory.SlReason, -clampedSL)
			}
		}
	}

	var buyResult models.ExchangeOrderResult
	if isBtcQuotePair(pair) || pair == "BTCUSDT" {
		buyResult, err = rt.Exchange.PlaceMarketBuyQuote(ctx, pair, size)
	} else {
		buyResult, err = rt.Exchange.PlaceMarketBuy(ctx, pair, size)
	}

	if err != nil {
		reply := fmt.Sprintf("Order failed: %v", err)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	text := fmt.Sprintf(
		"✅ *Order Placed — %s*\n"+
			"Pair: %s\n"+
			"Side: BUY\n"+
			"Size: %s\n"+
			"Order ID: %s\n"+
			"Status: %s\n\n"+
			"*Dynamic TP/SL from LLM:*\n"+
			"Take Profit: %s%% — %s\n"+
			"Stop Loss: %s%% — %s",
		utils.EscapeMdv2(rt.Exchange.ExchangeName()),
		utils.EscapeMdv2(pair),
		utils.EscapeMdv2(fmt.Sprintf("%.6f", size)),
		utils.EscapeMdv2(buyResult.OrderID),
		utils.EscapeMdv2(buyResult.Status),
		utils.EscapeMdv2(fmt.Sprintf("%.1f", advisory.DynamicTakeProfit)),
		utils.EscapeMdv2(advisory.TpReason),
		utils.EscapeMdv2(fmt.Sprintf("%.1f", advisory.DynamicStopLoss)),
		utils.EscapeMdv2(advisory.SlReason),
	)

	if buyResult.Status == "filled" || buyResult.Status == "new" {
		baseSize := size
		if currentPrice > 0.0 {
			baseSize = size / currentPrice
		}
		monitor.RecordPositionFromAdvisory(rt.Mem, &advisory, currentPrice, baseSize, pair, "buy")
		rt.Mem.DeductBalanceForBuy(pair, size)
	}

	_, err = utils.SendMdv2Safe(bot, chatID, text)
	return err
}

func (b *BtcBot) cmdSell(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	ts := rt.Mem.GetTreasuryState()
	if ts.TradingPausedUntil != "" {
		if paused, err := time.Parse(time.RFC3339, ts.TradingPausedUntil); err == nil {
			if time.Now().UTC().Before(paused.UTC()) {
				reply := fmt.Sprintf("⏸️ Trading is PAUSED until %s\nUse /btc_resume to resume.", paused.Format("2006-01-02 15:04 UTC"))
				_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
				return err
			}
		}
	}

	positions := rt.Mem.GetPositions()
	if len(positions) == 0 {
		_, err := bot.Send(tgbotapi.NewMessage(chatID, "No open positions to close"))
		return err
	}

	cfg := rt.Mem.GetConfig()
	var results []string

	if cfg.DryRun {
		for _, pos := range positions {
			btcPrice := btcPriceForConversion(ctx, rt.Exchange, pos.ID)
			if rt.Mem.UpdateTreasuryOnClose(pos.ID, pos.PnlBtc, pos.EntryPrice*pos.Size, btcPrice) {
				lesson := fmt.Sprintf("[BTC][MANUAL][DRY RUN] %s: PnL %.2f%%. Size: %.6f. Manual close.", pos.ID, pos.PnlBtc, pos.Size)
				rt.Mem.AddLesson(lesson)
				results = append(results, fmt.Sprintf("%s — PnL: %s%%", utils.EscapeMdv2(pos.ID), utils.EscapeMdv2(fmt.Sprintf("%.2f", pos.PnlBtc))))
			} else {
				results = append(results, fmt.Sprintf("%s — DRY RUN recorded (missing BTC price)", utils.EscapeMdv2(pos.ID)))
			}
		}
		rt.Mem.SavePositions([]models.BtcAdvisoryPosition{})
		reply := fmt.Sprintf("🧪 *DRY RUN — Simulated Close All*\n\n%s", strings.Join(results, "\n"))
		_, err := utils.SendMdv2Safe(bot, chatID, reply)
		return err
	}

	for _, pos := range positions {
		_, _ = rt.Exchange.CancelAll(ctx, pos.ID)
		order, err := rt.Exchange.PlaceMarketSell(ctx, pos.ID, pos.Size)
		if err == nil {
			btcPrice := btcPriceForConversion(ctx, rt.Exchange, pos.ID)
			rt.Mem.UpdateTreasuryOnClose(pos.ID, pos.PnlBtc, pos.EntryPrice*pos.Size, btcPrice)
			lesson := fmt.Sprintf("[BTC][MANUAL] %s: PnL %.2f%%. Size: %.6f. Manual close.", pos.ID, pos.PnlBtc, pos.Size)
			rt.Mem.AddLesson(lesson)
			results = append(results, fmt.Sprintf("✅ %s closed — %s @ %s \\| PnL: %s%%",
				utils.EscapeMdv2(pos.ID),
				utils.EscapeMdv2(fmt.Sprintf("%.6f", pos.Size)),
				utils.EscapeMdv2(order.OrderID),
				utils.EscapeMdv2(fmt.Sprintf("%.2f", pos.PnlBtc))))
		} else {
			results = append(results, fmt.Sprintf("❌ %s failed: %v", utils.EscapeMdv2(pos.ID), err))
		}
	}

	rt.Mem.SavePositions([]models.BtcAdvisoryPosition{})
	reply := fmt.Sprintf("*Close Results — %s*\n\n%s", utils.EscapeMdv2(rt.Exchange.ExchangeName()), strings.Join(results, "\n"))
	_, err := utils.SendMdv2Safe(bot, chatID, reply)
	return err
}

func (b *BtcBot) cmdClose(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	ts := rt.Mem.GetTreasuryState()
	if ts.TradingPausedUntil != "" {
		if paused, err := time.Parse(time.RFC3339, ts.TradingPausedUntil); err == nil {
			if time.Now().UTC().Before(paused.UTC()) {
				reply := fmt.Sprintf("⏸️ Trading is PAUSED until %s\nUse /btc_resume to resume.", paused.Format("2006-01-02 15:04 UTC"))
				_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
				return err
			}
		}
	}

	idxStr := strings.TrimSpace(args)
	if idxStr == "" {
		reply := "Usage: /btc_close <index>\nExample: /btc_close 1\n\nUse /btc_positions to see indices."
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	idx64, err := strconv.ParseInt(idxStr, 10, 32)
	if err != nil || idx64 < 1 {
		reply := fmt.Sprintf("Invalid index: '%s'. Must be a positive number.", idxStr)
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}
	idx := int(idx64) - 1

	positions := rt.Mem.GetPositions()
	if idx >= len(positions) {
		reply := fmt.Sprintf("Position #%d not found. You have %d open positions.", idx+1, len(positions))
		_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	pos := positions[idx]
	pair := pos.ID
	size := pos.Size
	entryPrice := pos.EntryPrice
	pnlPct := pos.PnlBtc

	cfg := rt.Mem.GetConfig()
	if cfg.DryRun {
		btcPrice := btcPriceForConversion(ctx, rt.Exchange, pair)
		if rt.Mem.UpdateTreasuryOnClose(pair, pnlPct, entryPrice*size, btcPrice) {
			lesson := fmt.Sprintf("[BTC][MANUAL][DRY RUN] %s: PnL %.2f%%. Size: %.6f. Manual close #%d.", pair, pnlPct, size, idx+1)
			rt.Mem.AddLesson(lesson)
		}
		// Remove position
		positions = append(positions[:idx], positions[idx+1:]...)
		rt.Mem.SavePositions(positions)

		reply := fmt.Sprintf("🧪 *DRY RUN* — Simulated close\n✅ \\#%s %s — size: %s \\| PnL: %s%%",
			utils.EscapeMdv2(fmt.Sprintf("%d", idx+1)),
			utils.EscapeMdv2(pair),
			utils.EscapeMdv2(fmt.Sprintf("%.6f", size)),
			utils.EscapeMdv2(fmt.Sprintf("%.2f", pnlPct)))
		_, err = utils.SendMdv2Safe(bot, chatID, reply)
		return err
	}

	_, _ = rt.Exchange.CancelAll(ctx, pair)
	result, err := rt.Exchange.PlaceMarketSell(ctx, pair, size)
	if err == nil {
		btcPrice := btcPriceForConversion(ctx, rt.Exchange, pair)
		rt.Mem.UpdateTreasuryOnClose(pair, pnlPct, entryPrice*size, btcPrice)
		lesson := fmt.Sprintf("[BTC][MANUAL] %s: PnL %.2f%%. Size: %.6f. Manual close #%d.", pair, pnlPct, size, idx+1)
		rt.Mem.AddLesson(lesson)

		positions = append(positions[:idx], positions[idx+1:]...)
		rt.Mem.SavePositions(positions)

		reply := fmt.Sprintf("✅ \\#%s %s closed — %s \\| PnL: %s%%",
			utils.EscapeMdv2(fmt.Sprintf("%d", idx+1)),
			utils.EscapeMdv2(pair),
			utils.EscapeMdv2(result.OrderID),
			utils.EscapeMdv2(fmt.Sprintf("%.2f", pnlPct)))
		_, err = utils.SendMdv2Safe(bot, chatID, reply)
		return err
	}

	reply := fmt.Sprintf("❌ Failed to close #%d %s: %v", idx+1, pair, err)
	_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
	return err
}

func (b *BtcBot) cmdCloseAll(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	return b.cmdSell(ctx, bot, chatID)
}

func (b *BtcBot) cmdDryRun(bot *tgbotapi.BotAPI, chatID int64, args string) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	arg := strings.TrimSpace(strings.ToLower(args))
	if arg == "" {
		cfg := rt.Mem.GetConfig()
		current := "OFF 🔴 (LIVE)"
		if cfg.DryRun {
			current = "ON 🧪 (simulation)"
		}
		reply := fmt.Sprintf("Dry Run is currently: %s\n\nUse:\n  /btc_dryrun on  — enable simulation\n  /btc_dryrun off — enable live trading", current)
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	cfg := rt.Mem.GetConfig()
	switch arg {
	case "on":
		cfg.DryRun = true
		rt.Mem.SaveConfig(cfg)
		reply := fmt.Sprintf("🧪 *DRY RUN enabled*\nAll trades will be simulated. No real orders on %s.", rt.Exchange.ExchangeName())
		_, err := utils.SendMdv2Safe(bot, chatID, reply)
		return err
	case "off":
		cfg.DryRun = false
		rt.Mem.SaveConfig(cfg)
		reply := fmt.Sprintf("🔴 *LIVE TRADING enabled*\n⚠️ All orders WILL execute on %s!", rt.Exchange.ExchangeName())
		_, err := utils.SendMdv2Safe(bot, chatID, reply)
		return err
	default:
		reply := fmt.Sprintf("Invalid: '%s'. Use 'on' or 'off'.\n\n/btc_dryrun on  — simulation\n/btc_dryrun off — live trading", arg)
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}
}

func (b *BtcBot) cmdPause(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	ts := rt.Mem.GetTreasuryState()
	pausedUntil := time.Now().UTC().Add(24 * time.Hour)
	ts.TradingPausedUntil = pausedUntil.Format(time.RFC3339)
	rt.Mem.SaveTreasuryState(ts)

	reply := fmt.Sprintf("⏸️ *Trading PAUSED*\nResumes: %s\n\nAll buy/sell commands and auto-execution are blocked until then.",
		utils.EscapeMdv2(pausedUntil.Format("2006-01-02 15:04 UTC")))
	_, err := utils.SendMdv2Safe(bot, chatID, reply)
	return err
}

func (b *BtcBot) cmdResume(bot *tgbotapi.BotAPI, chatID int64) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "Exchange not configured")
		return err
	}

	ts := rt.Mem.GetTreasuryState()
	ts.TradingPausedUntil = ""
	rt.Mem.SaveTreasuryState(ts)

	_, err := utils.SendMdv2Safe(bot, chatID, "▶️ *Trading RESUMED*\nAll commands and auto-execution are now active.")
	return err
}

func (b *BtcBot) cmdUse(bot *tgbotapi.BotAPI, chatID int64, args string) error {
	raw := strings.TrimSpace(args)
	parts := strings.Fields(raw)

	if len(parts) == 0 {
		reply := "Usage:\n  /btc_use <account_id>             — bind to first exchange under id\n  /btc_use <account_id> <exchange>   — bind to specific exchange (binance | okx)\n\nUse /btc_accounts to list configured bindings."
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	id := parts[0]
	var exchangeArg string
	if len(parts) >= 2 {
		exchangeArg = strings.ToLower(parts[1])
	}

	var selectedKey exchange.AccountKey
	found := false

	if exchangeArg != "" {
		kind, err := config.ParseExchangeKind(exchangeArg)
		if err != nil {
			var available []string
			seen := make(map[config.ExchangeKind]bool)
			for k := range b.perAccount {
				if !seen[k.Exchange] {
					seen[k.Exchange] = true
					available = append(available, string(k.Exchange))
				}
			}
			reply := fmt.Sprintf("Unknown exchange '%s'. Available: %s", exchangeArg, strings.Join(available, ", "))
			_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
			return err
		}

		key := exchange.AccountKey{AccountID: id, Exchange: kind}
		if _, ok := b.perAccount[key]; ok {
			selectedKey = key
			found = true
		} else {
			var bindings []string
			for k := range b.perAccount {
				if k.AccountID == id {
					bindings = append(bindings, string(k.Exchange))
				}
			}
			reply := fmt.Sprintf("No binding for '%s/%s'. Available under '%s': [%s]", id, exchangeArg, id, strings.Join(bindings, ", "))
			_, err = bot.Send(tgbotapi.NewMessage(chatID, reply))
			return err
		}
	} else {
		for k := range b.perAccount {
			if k.AccountID == id {
				selectedKey = k
				found = true
				break
			}
		}
		if !found {
			var available []string
			for k := range b.perAccount {
				available = append(available, fmt.Sprintf("%s/%s", k.AccountID, k.Exchange))
			}
			reply := fmt.Sprintf("Account '%s' not configured. Available: %s", id, strings.Join(available, ", "))
			_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
			return err
		}
	}

	if found {
		b.activeAccLock.Lock()
		b.activeAccount[chatID] = selectedKey
		b.activeAccLock.Unlock()

		reply := fmt.Sprintf("✅ Active binding for this chat: *%s* on *%s*", utils.EscapeMdv2(selectedKey.AccountID), selectedKey.Exchange)
		_, err := utils.SendMdv2Safe(bot, chatID, reply)
		return err
	}

	return nil
}

func (b *BtcBot) cmdAccounts(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64) error {
	if len(b.perAccount) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No accounts configured. Set up accounts.json or BINANCE_API_KEY/EXCHANGE_API_SECRET.")
		return err
	}

	b.activeAccLock.RLock()
	current, exists := b.activeAccount[chatID]
	b.activeAccLock.RUnlock()

	if !exists {
		for k := range b.perAccount {
			current = k
			break
		}
	}

	distinctIDs := make(map[string]bool)
	for k := range b.perAccount {
		distinctIDs[k.AccountID] = true
	}

	lines := []string{fmt.Sprintf("📋 *Bindings* (%d binding(s) across %d account(s))\n", len(b.perAccount), len(distinctIDs))}

	for key, rt := range b.perAccount {
		isActive := key == current
		marker := "  "
		if isActive {
			marker = "▶️"
		}
		api := rt.Exchange.APIKeyDisplay()
		balances, err := rt.Exchange.GetBalances(ctx)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s *%s/%s* — `%s` (balance fetch failed: %v)",
				marker, utils.EscapeMdv2(key.AccountID), key.Exchange, utils.EscapeMdv2(api), err))
			continue
		}

		var btc, usdt float64
		for _, bal := range balances {
			if bal.Asset == "BTC" {
				btc = bal.Free + bal.Locked
			}
			if bal.Asset == "USDT" || bal.Asset == "USDC" {
				usdt = bal.Free + bal.Locked
			}
		}

		lines = append(lines, fmt.Sprintf(
			"%s *%s/%s* — `%s`\n     BTC: %s \\| USDT: %s",
			marker,
			utils.EscapeMdv2(key.AccountID), key.Exchange,
			utils.EscapeMdv2(api),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", btc)),
			utils.EscapeMdv2(fmt.Sprintf("%.2f", usdt)),
		))
	}

	lines = append(lines, "", fmt.Sprintf("Active: *%s/%s*\nUse /btc_use <id> [exchange] to switch.",
		utils.EscapeMdv2(current.AccountID), current.Exchange))

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdAggregate(bot *tgbotapi.BotAPI, chatID int64) error {
	if len(b.perAccount) == 0 {
		_, err := utils.SendMdv2Safe(bot, chatID, "No accounts configured")
		return err
	}

	var totalBTC, totalVault, totalCompound float64
	var totalTrades, totalWins uint64
	var lines []string
	lines = append(lines, "📊 *Aggregate — All Bindings*\n")

	var keys []exchange.AccountKey
	for k := range b.perAccount {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].AccountID != keys[j].AccountID {
			return keys[i].AccountID < keys[j].AccountID
		}
		return keys[i].Exchange < keys[j].Exchange
	})

	for _, key := range keys {
		rt := b.perAccount[key]
		ts := rt.Mem.GetTreasuryState()
		totalBTC += ts.CurrentBtc
		totalVault += ts.BtcTreasuryVault
		totalCompound += ts.CompoundBalance
		totalTrades += uint64(ts.TotalTrades)
		totalWins += uint64(ts.WinningTrades)

		var winRate float64
		if ts.TotalTrades > 0 {
			winRate = float64(ts.WinningTrades) / float64(ts.TotalTrades) * 100.0
		}

		lines = append(lines, fmt.Sprintf(
			"*%s/%s*: BTC %s \\| Vault %s \\| Trades %s \\(win %s%%\\)",
			utils.EscapeMdv2(key.AccountID), key.Exchange,
			utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CurrentBtc)),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.BtcTreasuryVault)),
			utils.EscapeMdv2(fmt.Sprintf("%d", ts.TotalTrades)),
			utils.EscapeMdv2(fmt.Sprintf("%.0f", winRate)),
		))
	}

	var overallWR float64
	if totalTrades > 0 {
		overallWR = float64(totalWins) / float64(totalTrades) * 100.0
	}

	lines = append(lines, "", fmt.Sprintf(
		"*Total*\nBTC: %s\nVault: %s\nCompound: %s\nTrades: %s \\(win %s%%\\)",
		utils.EscapeMdv2(fmt.Sprintf("%.8f", totalBTC)),
		utils.EscapeMdv2(fmt.Sprintf("%.8f", totalVault)),
		utils.EscapeMdv2(fmt.Sprintf("%.8f", totalCompound)),
		utils.EscapeMdv2(fmt.Sprintf("%d", totalTrades)),
		utils.EscapeMdv2(fmt.Sprintf("%.0f", overallWR)),
	))

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func renderStatus(ctx context.Context, runtimes []*runtime.AccountRuntime) (string, error) {
	if len(runtimes) == 0 {
		return "No exchange configured", nil
	}
	multi := len(runtimes) > 1
	var blocks []string
	var aggBtc, aggVault, aggCompound float64
	var aggTrades, aggWins uint64

	// Get BTC price for USD conversion
	btcPrice := 0.0
	if len(runtimes) > 0 {
		p, err := runtimes[0].Exchange.GetCurrentPrice(ctx, "BTCUSDT")
		if err == nil && p > 0 {
			btcPrice = p
		}
	}

	for _, rt := range runtimes {
		balances, err := rt.Exchange.GetBalances(ctx)
		if err != nil {
			blocks = append(blocks, fmt.Sprintf("💼 *[%s/%s]* — balance fetch failed: %s",
				utils.EscapeMdv2(rt.AccountID), rt.Spec.Exchange, utils.EscapeMdv2(err.Error())))
			continue
		}

		var stableFree, stableLocked float64
		stableAsset := "USDT"
		hasUSDC := false
		for _, bal := range balances {
			if bal.Asset == "USDC" {
				hasUSDC = true
			}
			if bal.Asset == "USDT" || bal.Asset == "USDC" {
				stableFree = bal.Free
				stableLocked = bal.Locked
			}
		}
		if hasUSDC {
			stableAsset = "USDC"
		}

		ts := rt.Mem.GetTreasuryState()
		cfg := rt.Mem.GetConfig()
		aggBtc += ts.CurrentBtc
		aggVault += ts.BtcTreasuryVault
		aggCompound += ts.CompoundBalance
		aggTrades += uint64(ts.TotalTrades)
		aggWins += uint64(ts.WinningTrades)

		var header string
		if multi {
			header = fmt.Sprintf("💼 *Account — [%s/%s]*", utils.EscapeMdv2(rt.AccountID), rt.Spec.Exchange)
		} else {
			header = fmt.Sprintf("💼 *Account — %s*", utils.EscapeMdv2(rt.Exchange.ExchangeName()))
		}

		// Health check
		hb := rt.Status.HeartbeatUnix()
		restarts := rt.Status.Restarts()
		healthIcon := "⚠️"
		healthText := "No heartbeat"
		if hb > 0 {
			ageSecs := time.Now().Unix() - hb
			if ageSecs < 0 {
				ageSecs = 0
			}
			if ageSecs < 120 {
				healthIcon = "✅"
			} else if ageSecs < 300 {
				healthIcon = "⚡"
			}
			restartTxt := ""
			if restarts > 0 {
				restartTxt = fmt.Sprintf(" | ⚠️ Restarts: %d", restarts)
			}
			healthText = fmt.Sprintf("tick %ds ago%s", ageSecs, restartTxt)
		}

		// Win rate
		var winRate float64
		if ts.TotalTrades > 0 {
			winRate = float64(ts.WinningTrades) / float64(ts.TotalTrades) * 100.0
		}

		// BTC value in USD
		btcValueUsd := ts.CurrentBtc * btcPrice

		lines := []string{
			header,
			fmt.Sprintf("Mode: %s | Health: %s %s",
				func() string {
					if cfg.DryRun {
						return "🧪 DRY RUN"
					}
					return "🔴 LIVE"
				}(),
				healthIcon,
				healthText),
			fmt.Sprintf("API: `%s`", utils.EscapeMdv2(rt.Exchange.APIKeyDisplay())),
			"",
			"📊 *Balance*",
			fmt.Sprintf("%s: %s \\(%.2f USD\\) | %s locked",
				utils.EscapeMdv2(stableAsset),
				utils.EscapeMdv2(fmt.Sprintf("%.2f", stableFree)),
				stableFree,
				utils.EscapeMdv2(fmt.Sprintf("%.2f", stableLocked))),
		}

		// BTC info with USD value
		if btcPrice > 0 {
			lines = append(lines, fmt.Sprintf("BTC: %s \\(≈%.2f USD\\)",
				utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CurrentBtc)),
				btcValueUsd))
		} else {
			lines = append(lines, fmt.Sprintf("BTC: %s",
				utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CurrentBtc))))
		}

		lines = append(lines, "", "🏦 *Treasury*")
		lines = append(lines, fmt.Sprintf("Total BTC: %s", utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CurrentBtc))))
		lines = append(lines, fmt.Sprintf("Vault: %s \\(untouchable\\)", utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.BtcTreasuryVault))))
		lines = append(lines, fmt.Sprintf("Compound: %s", utils.EscapeMdv2(fmt.Sprintf("%.8f", ts.CompoundBalance))))

		if btcPrice > 0 {
			lines = append(lines, fmt.Sprintf("Vault Value: ≈%.2f USD", ts.BtcTreasuryVault*btcPrice))
		}

		// Growth stats
		growthIcon := "📈"
		if ts.BtcGrowth7d < 0 {
			growthIcon = "📉"
		}
		lines = append(lines, "", "📈 *Performance*")
		lines = append(lines, fmt.Sprintf("%s 7d Growth: %s%%", growthIcon, utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.BtcGrowth7d*100.0))))
		lines = append(lines, fmt.Sprintf("30d Growth: %s%%", utils.EscapeMdv2(fmt.Sprintf("%.2f", ts.BtcGrowth30d*100.0))))
		lines = append(lines, fmt.Sprintf("Trades: %d \\(Win: %d | Loss: %d\\) — WR: %s%%",
			ts.TotalTrades,
			ts.WinningTrades,
			ts.LosingTrades,
			utils.EscapeMdv2(fmt.Sprintf("%.0f", winRate))))

		// Pause status
		if ts.TradingPausedUntil != "" {
			if paused, err := time.Parse(time.RFC3339, ts.TradingPausedUntil); err == nil {
				if time.Now().UTC().Before(paused.UTC()) {
					lines = append(lines, "", fmt.Sprintf("⏸️ *Paused until:* %s", utils.EscapeMdv2(paused.Format("2006-01-02 15:04 UTC"))))
				}
			}
		}

		// Open positions from memory
		positions := rt.Mem.GetPositions()
		if len(positions) > 0 {
			lines = append(lines, "", "📂 *Open Positions*")
			for i, p := range positions {
				pnlIcon := "📊"
				if p.PnlBtc > 0 {
					pnlIcon = "🟢"
				} else if p.PnlBtc < 0 {
					pnlIcon = "🔴"
				}
				lines = append(lines, fmt.Sprintf("%d\\. %s %s — Entry: %s | PnL: %s%%",
					i+1,
					pnlIcon,
					utils.EscapeMdv2(p.ID),
					utils.EscapeMdv2(fmt.Sprintf("%.8f", p.EntryPrice)),
					utils.EscapeMdv2(fmt.Sprintf("%.2f", p.PnlBtc))))
			}
		}

		// Open orders from exchange
		pairs := rt.ScannerState.GetPairs()
		var allOrders []models.BtcAdvisoryPosition
		for _, p := range pairs {
			orders, err := rt.Exchange.GetOpenOrders(ctx, p)
			if err == nil {
				allOrders = append(allOrders, orders...)
			}
		}

		if len(allOrders) > 0 {
			lines = append(lines, "", "📋 *Exchange Orders*")
			for _, o := range allOrders {
				sideIcon := "📤"
				if strings.ToLower(o.Side) == "buy" {
					sideIcon = "📥"
				}
				lines = append(lines, fmt.Sprintf("%s %s: %s @ %s | TP: %s%% | SL: %s%%",
					sideIcon,
					utils.EscapeMdv2(o.ID),
					utils.EscapeMdv2(fmt.Sprintf("%.6f", o.Size)),
					utils.EscapeMdv2(fmt.Sprintf("%.8f", o.EntryPrice)),
					utils.EscapeMdv2(fmt.Sprintf("%.1f", o.TakeProfitPct)),
					utils.EscapeMdv2(fmt.Sprintf("%.1f", o.StopLossPct))))
			}
		}

		// Scanner stats
		snapshots := rt.ScannerState.AllSnapshots()
		if len(snapshots) > 0 {
			var totalScanned int
			var approveCnt, monitorCnt, protectCnt, rejectCnt int
			for _, s := range snapshots {
				totalScanned += int(s.Stats.Scanned)
				switch s.LastRecommendation {
				case "APPROVE":
					approveCnt++
				case "MONITOR":
					monitorCnt++
				case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
					protectCnt++
				default:
					if s.LastRecommendation != "" {
						rejectCnt++
					}
				}
			}
			lines = append(lines, "", "🔍 *Scanner*")
			lines = append(lines, fmt.Sprintf("Pairs: %d | Scans: %d", len(snapshots), totalScanned))
			lines = append(lines, fmt.Sprintf("✅ %d | 👁 %d | 🛡 %d | ❌ %d",
				approveCnt, monitorCnt, protectCnt, rejectCnt))
		}

		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	if multi {
		var overallWR float64
		if aggTrades > 0 {
			overallWR = float64(aggWins) / float64(aggTrades) * 100.0
		}
		totalUsd := aggBtc * btcPrice
		footer := fmt.Sprintf(
			"\n──────────\n*Aggregate — [%s]*\nBTC: %s \\(≈%.2f USD\\) | Vault: %s | Compound: %s\nTrades: %d \\(win %s%%\\)",
			utils.EscapeMdv2(runtimes[0].AccountID),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", aggBtc)),
			totalUsd,
			utils.EscapeMdv2(fmt.Sprintf("%.8f", aggVault)),
			utils.EscapeMdv2(fmt.Sprintf("%.8f", aggCompound)),
			aggTrades,
			utils.EscapeMdv2(fmt.Sprintf("%.0f", overallWR)),
		)
		blocks = append(blocks, footer)
	}

	// Add BTC price footer if available
	if btcPrice > 0 {
		blocks = append(blocks, fmt.Sprintf("\n*BTC Price:* %.2f USD", btcPrice))
	}

	return strings.Join(blocks, "\n\n"), nil
}

func (b *BtcBot) cmdReport(bot *tgbotapi.BotAPI, chatID int64) error {
	var lines []string
	lines = append(lines, "📊 *Report Configuration*")

	// Report interval
	if b.reportInterval == 0 {
		lines = append(lines, "Status: ❌ Disabled")
	} else {
		lines = append(lines, "Status: ✅ Enabled")
		lines = append(lines, fmt.Sprintf("Interval: %d min", b.reportInterval))
	}

	// Report chat IDs info
	lines = append(lines, "", "*Report Chat IDs*")
	lines = append(lines, "Configure via TELEGRAM_REPORT_CHAT_IDS env var")
	lines = append(lines, "Format: TELEGRAM_REPORT_CHAT_IDS=123,456,789")
	lines = append(lines, "Restart service after changing env var")

	// Scanner interval
	lines = append(lines, "", "*Scanner Interval*")
	lines = append(lines, "Configure via BTC_SCANNER_INTERVAL_SECS env var (default: 900 = 15 min)")

	lines = append(lines, "", "*Set Report Interval*")
	lines = append(lines, "/btc_setreport 0 — disable reports")
	lines = append(lines, "/btc_setreport 5 — every 5 minutes")
	lines = append(lines, "/btc_setreport 15 — every 15 minutes")
	lines = append(lines, "/btc_setreport 60 — every hour")

	_, err := utils.SendMdv2Safe(bot, chatID, strings.Join(lines, "\n"))
	return err
}

func (b *BtcBot) cmdSetReport(bot *tgbotapi.BotAPI, chatID int64, args string) error {
	raw := strings.TrimSpace(args)
	if raw == "" {
		reply := "Usage: /btc_setreport <interval_minutes>\n\n" +
			"  /btc_setreport 0  — disable periodic reports\n" +
			"  /btc_setreport 5  — every 5 minutes\n" +
			"  /btc_setreport 15 — every 15 minutes\n" +
			"  /btc_setreport 60 — every hour\n\n" +
			"Note: This setting is runtime-only. For persistent config, set BTC_REPORT_INTERVAL_MINS env var before starting the service."
		_, err := bot.Send(tgbotapi.NewMessage(chatID, reply))
		return err
	}

	interval, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || interval < 0 || interval > 1440 {
		_, err := bot.Send(tgbotapi.NewMessage(chatID, "Invalid interval. Must be 0-1440 minutes (0 = disable)."))
		return err
	}

	b.reportInterval = uint64(interval)

	var reply string
	if interval == 0 {
		reply = "✅ *Reports disabled*\nPeriodic reports are now OFF."
	} else {
		reply = fmt.Sprintf("✅ *Report interval set to %d min*\nPeriodic reports enabled.", interval)
	}
	_, err = utils.SendMdv2Safe(bot, chatID, reply)
	return err
}

func (b *BtcBot) getConfigForChat(chatID int64) models.BtcConfig {
	rt := b.resolveRuntime(chatID)
	if rt != nil {
		return rt.Mem.GetConfig()
	}
	return models.BtcConfig{}
}

func btcPriceForConversion(ctx context.Context, ex exchange.ExchangeClient, pair string) float64 {
	if isBtcQuotePair(pair) {
		return 1.0
	}
	p, err := ex.GetCurrentPrice(ctx, "BTCUSDT")
	if err != nil {
		log.Printf("Failed to fetch conversion price for BTCUSDT: %v", err)
		return 0.0
	}
	return p
}

func scoreBar(score float64) string {
	filled := int(math.Round(score / 10.0))
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	empty := 10 - filled
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	return fmt.Sprintf("[%s] ", bar)
}

func isBtcQuotePair(pair string) bool {
	p := strings.ToUpper(pair)
	return strings.HasSuffix(p, "BTC") && p != "BTCUSDT"
}

func validatePairToken(raw string) (string, error) {
	pair := strings.TrimSpace(strings.ToUpper(raw))
	if pair == "" {
		return "", fmt.Errorf("pair name is empty")
	}
	if len(pair) > 15 {
		return "", fmt.Errorf("'%s' is not a valid pair name (max 15 alphanumeric chars)", raw)
	}
	for _, c := range pair {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", fmt.Errorf("'%s' is not a valid pair name (max 15 alphanumeric chars)", raw)
		}
	}
	if !strings.HasSuffix(pair, "BTC") {
		return "", fmt.Errorf("'%s' is not a BTC-quote pair (must end with BTC, e.g. SOLBTC, ETHBTC)", pair)
	}
	return pair, nil
}

// cmdSetCreds updates API credentials for the active account/exchange.
// Usage: /btc_setcreds <api_key> <api_secret> [passphrase]
// OKX requires passphrase; Binance does not.
// Credentials are saved to btc-accounts.json (JSON store) or account_exchanges table (DB store).
// The exchange client is hot-reloaded in-memory immediately after saving.
func (b *BtcBot) cmdSetCreds(_ context.Context, bot *tgbotapi.BotAPI, chatID int64, args string) error {
	rt := b.resolveRuntime(chatID)
	if rt == nil {
		_, err := utils.SendMdv2Safe(bot, chatID, "No account bound. Use /btc_use first.")
		return err
	}

	parts := strings.Fields(args)
	if len(parts) < 2 {
		usage := "Usage: `/btc_setcreds <api_key> <api_secret> [passphrase]`\n\n" +
			"OKX requires passphrase. Binance does not.\n" +
			"Example \\(Binance\\): `/btc_setcreds ABCD1234 mysecret`\n" +
			"Example \\(OKX\\): `/btc_setcreds ABCD1234 mysecret mypassphrase`"
		_, err := utils.SendMdv2Safe(bot, chatID, usage)
		return err
	}

	apiKey := parts[0]
	apiSecret := parts[1]
	passphrase := ""
	if len(parts) >= 3 {
		passphrase = parts[2]
	}

	// OKX requires passphrase
	if rt.Spec.Exchange == "okx" && passphrase == "" {
		_, err := utils.SendMdv2Safe(bot, chatID, "OKX requires a passphrase. Usage: `/btc_setcreds <api_key> <api_secret> <passphrase>`")
		return err
	}

	// Validate key lengths (basic sanity check)
	if len(apiKey) < 8 || len(apiSecret) < 8 {
		_, err := utils.SendMdv2Safe(bot, chatID, "api_key and api_secret must be at least 8 characters.")
		return err
	}

	// Save credentials to persistent store
	if err := rt.Mem.UpdateExchangeCredentials(apiKey, apiSecret, passphrase); err != nil {
		log.Printf("cmdSetCreds: store update failed: %v", err)
		_, sendErr := utils.SendMdv2Safe(bot, chatID, fmt.Sprintf("Failed to save credentials: %s", utils.EscapeMdv2(err.Error())))
		return sendErr
	}

	// Hot-reload exchange client with new credentials
	newSpec := rt.Spec
	newSpec.Credentials.ApiKey = apiKey
	newSpec.Credentials.ApiSecret = apiSecret
	newSpec.Credentials.Passphrase = passphrase
	newSpec.Credentials.KeyEnv = ""
	newSpec.Credentials.SecretEnv = ""
	newSpec.Credentials.PassphraseEnv = ""

	newClient, err := exchange.BuildClientForSpec(&newSpec)
	if err != nil {
		log.Printf("cmdSetCreds: failed to build exchange client: %v", err)
		_, sendErr := utils.SendMdv2Safe(bot, chatID, fmt.Sprintf(
			"Credentials saved, but failed to reload exchange client: %v\nRestart service to apply.", err,
		))
		return sendErr
	}

	// Swap exchange client on the running runtime
	rt.Exchange = newClient
	if rt.Executor != nil {
		rt.Executor.UpdateExchange(newClient)
	}

	// Masked display: show first 4 + last 4 chars
	masked := func(s string) string {
		if len(s) <= 8 {
			return "****"
		}
		return s[:4] + "..." + s[len(s)-4:]
	}

	reply := fmt.Sprintf(
		"✅ *Credentials updated \\(%s / %s\\)*\n\nAPI Key: `%s`\nAPI Secret: `%s`",
		utils.EscapeMdv2(string(rt.Spec.Exchange)),
		utils.EscapeMdv2(rt.AccountID),
		utils.EscapeMdv2(masked(apiKey)),
		utils.EscapeMdv2(masked(apiSecret)),
	)
	if passphrase != "" {
		reply += fmt.Sprintf("\nPassphrase: `%s`", utils.EscapeMdv2(masked(passphrase)))
	}
	reply += "\n\nExchange client reloaded in\\-memory\\. Scanner will use new credentials on next cycle\\."

	_, err = utils.SendMdv2Safe(bot, chatID, reply)
	return err
}
