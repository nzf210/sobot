use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use teloxide::prelude::*;
use tokio::sync::RwLock;
use tokio::time::{interval, Duration};

use crate::engine::AdvisoryEngine;
use crate::exchange::ExchangeClient;
use crate::format::{bot_send_plain, escape_mdv2, send_mdv2_safe};
use crate::memory::MemoryStore;
use crate::models::*;
use crate::position_monitor::record_position_from_advisory;
use crate::scanner::ScannerState;

// ── Static help / skills text ────────────────────────────────────────────────
// Binance Spot-focused. Pair names are Binance-style: SYMBOLBTC, ETHBTC, SOLBTC, etc.

const HELP_TEXT: &str = r#"🤖 *BTC Treasury Accumulation* — Binance Spot

*Account & Balances*
/btc\_status — Spot balance \(USDT \+ all assets\), open orders

*Market & Analysis*
/btc\_market \[PAIR\] — Live market data + OHLCV summary
/btc\_advisory \[PAIR\] — Full quant \+ LLM advisory
/btc\_scan \[PAIR\] — Scanner stats per pair \(AI scores\)

*Treasury & Positions*
/btc\_treasury — BTC holdings, vault, compound balance, trade stats
/btc\_positions — Open positions with TP/SL/trailing

*Pair Management \(Binance BTC‑Quote\)*
/btc\_pairs — List active scanned pairs
/btc\_addpair \<PAIR\> — Add pair \(e\.g\. SOLBTC, ETHBTC, SUIBTC\)
/btc\_removepair \<PAIR\> — Remove pair from scanner
/btc\_discover — Auto\-discover all BTC\-quote pairs on Binance
/btc\_pairinfo \<PAIR\> — AI scores for one pair

*History & Learning*
/btc\_history — Last 10 decisions
/btc\_lessons — Recent self\-learning lessons

*Trading \(Binance Spot\)*
/btc\_buy \<SIZE\> \<PAIR\> — Market buy with dynamic TP/SL
/btc\_sell — Close ALL positions at market price
/btc\_close \<index\> — Close position by index \(1\-based\)
/btc\_closeall — Force close all positions
/btc\_cancel — Cancel all open orders

*Bot Control*
/btc\_dryrun on\|off — Toggle dry run mode \(simulation\)
/btc\_pause — Pause trading \(24h\) 
/btc\_resume — Resume trading

*Configuration*
/btc\_config — Current config \(TP/SL/thresholds\)
/btc\_setconfig \<key\> \<value\> — Update config live
/btc\_enable — Enable LLM advisory
/btc\_disable — Disable LLM advisory

*Info*
/btc\_skills — Full bot capabilities
/help — This message

*Pair Format \(Binance BTC‑Quote\)*
Examples: SOLBTC, ETHBTC, SUIBTC, LINKBTC, DOGEBTC, ADABTC
Auto\-discover with /btc\_discover"#;

const SKILLS_TEXT: &str = r#"*BTC Treasury Accumulation — Skills*

*1\. Binance Spot Scanner*
- Poll interval: every 15 min \(configurable\)
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
| Component | Weight |
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
- Position size: risk\_amount \+ SL distance

*7\. Entry Conditions \(ALL must pass\)*
✅ RS Rising \(1h RS \* 4h RS\)
✅ EMA20 \* EMA50 \* EMA200 bullish
✅ MACD bullish
✅ Volume \* Average
✅ AI Score \* 80

*8\. Exit Conditions*
- Take Profit: 3\-8% \(dynamic\)
- Trailing Stop: track peak, trigger on X% drop
- Stop Loss: 1\-2% \(hard limit\)
- TP \* |SL| always maintained

*9\. BTC Treasury Split*
On every winning close:
- 50% → BTC Treasury Vault \(never traded\)
- 50% → Compound balance \(re‑enter capital\)

*10\. Anti\-FOMO*
❌ Martingale
❌ Averaging Down
❌ Revenge Trading
❌ YOLO / All\-In

*Exchange: Binance Spot only — NO futures, NO perpetual, NO leverage*"#;

// ── BtcBot ───────────────────────────────────────────────────────────────────

pub struct BtcBot {
    token: String,
    whitelist: Vec<i64>,
    engine: Arc<AdvisoryEngine>,
    mem: Arc<MemoryStore>,
    exchange: Option<Arc<dyn ExchangeClient>>,
    scanner: Option<Arc<ScannerState>>,
}

impl Clone for BtcBot {
    fn clone(&self) -> Self {
        Self {
            token: self.token.clone(),
            whitelist: self.whitelist.clone(),
            engine: Arc::clone(&self.engine),
            mem: Arc::clone(&self.mem),
            exchange: self.exchange.clone(),
            scanner: self.scanner.clone(),
        }
    }
}

impl BtcBot {
    pub fn new(
        token: String,
        whitelist: Vec<i64>,
        engine: Arc<AdvisoryEngine>,
        mem: Arc<MemoryStore>,
        exchange: Option<Arc<dyn ExchangeClient>>,
        scanner: Option<Arc<ScannerState>>,
    ) -> Self {
        Self { token, whitelist, engine, mem, exchange, scanner }
    }

    // ── lifecycle ──────────────────────────────────────────────────────────

    pub async fn start(self: Arc<Self>) {
        loop {
            match self.run_bot().await {
                Ok(_) => tracing::info!("Telegram bot stopped cleanly"),
                Err(e) => {
                    tracing::error!("Telegram bot error, restarting in 5s: {}", e);
                    tokio::time::sleep(std::time::Duration::from_secs(5)).await;
                }
            }
        }
    }

    async fn run_bot(&self) -> anyhow::Result<()> {
        let bot = Bot::new(&self.token);
        let this = Arc::new(self.clone());
        tracing::info!("BTC Treasury Telegram bot started");

        teloxide::repl(bot, move |bot: Bot, msg: Message| {
            let this = Arc::clone(&this);
            async move {
                if let Some(text) = msg.text() {
                    let text = text.to_string();
                    this.handle_message(&bot, &msg, &text).await;
                }
                Ok(())
            }
        })
        .await;

        Ok(())
    }

    // ── routing ────────────────────────────────────────────────────────────

    fn is_whitelisted(&self, user_id: i64) -> bool {
        self.whitelist.is_empty() || self.whitelist.contains(&user_id)
    }

    async fn handle_message(&self, bot: &Bot, msg: &Message, text: &str) {
        let user_id = msg.chat.id.0;
        if !self.is_whitelisted(user_id) {
            let _ = bot.send_message(msg.chat.id, "⛔ Unauthorized").await;
            return;
        }

        let text = text.trim();
        let (cmd, args) = if let Some(rest) = text.strip_prefix('/') {
            let parts: Vec<&str> = rest.splitn(2, |c: char| c.is_whitespace()).collect();
            let mut cmd = parts[0].to_lowercase();
            cmd = cmd.replace('_', "");
            let rest = parts.get(1).map(|s| s.trim().to_string());
            (cmd, rest)
        } else {
            return;
        };

        let result = match cmd.as_str() {
            "help" => self.cmd_help(bot, msg).await,
            "btcstatus" => self.cmd_status(bot, msg).await,
            "btcmarket" => self.cmd_market(bot, msg, args).await,
            "btcadvisory" => self.cmd_advisory(bot, msg, args).await,
            "btctreasury" => self.cmd_treasury(bot, msg).await,
            "btcpositions" => self.cmd_positions(bot, msg).await,
            "btcscan" => self.cmd_scan(bot, msg, args).await,
            "btchistory" => self.cmd_history(bot, msg).await,
            "btclessons" => self.cmd_lessons(bot, msg).await,
            "btcskills" => self.cmd_skills(bot, msg).await,
            "btcpairs" => self.cmd_pairs(bot, msg).await,
            "btcaddpair" => self.cmd_addpair(bot, msg, args).await,
            "btcremovepair" => self.cmd_removepair(bot, msg, args).await,
            "btcdiscover" => self.cmd_discover(bot, msg).await,
            "btcpairinfo" => self.cmd_pairinfo(bot, msg, args).await,
            "btcconfig" => self.cmd_config(bot, msg).await,
            "btcsetconfig" => {
                if let Some(args_str) = args {
                    let parts: Vec<&str> = args_str.splitn(2, |c: char| c.is_whitespace()).collect();
                    if parts.len() == 2 {
                        self.cmd_setconfig(bot, msg, parts[0], parts[1]).await
                    } else {
                        bot_send_plain(bot, msg, "Usage: /btc_setconfig <key> <value>").await
                    }
                } else {
                    bot_send_plain(bot, msg, "Usage: /btc_setconfig <key> <value>").await
                }
            }
            "btcenable" => self.cmd_enable(bot, msg).await,
            "btcdisable" => self.cmd_disable(bot, msg).await,
            "btcbuy" => self.cmd_buy(bot, msg, args).await,
            "btcsell" => self.cmd_sell(bot, msg, args).await,
            "btcclose" => self.cmd_close(bot, msg, args).await,
            "btccloseall" => self.cmd_closeall(bot, msg, args).await,
            "btccancel" => self.cmd_cancel(bot, msg).await,
            "btcdryrun" => self.cmd_dryrun(bot, msg, args).await,
            "btcpause" => self.cmd_pause(bot, msg).await,
            "btcresume" => self.cmd_resume(bot, msg).await,
            "start" => self.cmd_help(bot, msg).await,
            _ => bot_send_plain(bot, msg, "Unknown command. Use /help").await,
        };

        if let Err(e) = result {
            tracing::error!("Command error: {}", e);
            let _ = bot_send_plain(bot, msg, &format!("Error: {}", e)).await;
        }
    }

    // ── commands ───────────────────────────────────────────────────────────

    /// /help — static markdown, no escaping needed.
    async fn cmd_help(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        send_mdv2_safe(bot, msg.chat.id, HELP_TEXT).await?;
        Ok(())
    }

    /// /btc_status — Binance Spot balance (USDT + all assets), open orders.
    async fn cmd_status(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let text = if let Some(ref exchange) = self.exchange {
            match exchange.get_balances().await {
                Ok(balances) => {
                    let stable_bal = balances.iter().find(|b| b.asset == "USDT" || b.asset == "USDC");
                    let stable_free = stable_bal.map(|b| b.free).unwrap_or(0.0);
                    let stable_locked = stable_bal.map(|b| b.locked).unwrap_or(0.0);
                    let stable_asset = if balances.iter().any(|b| b.asset == "USDC") { "USDC" } else { "USDT" };

                    let ts = self.mem.get_treasury_state();
                    let cfg = self.mem.get_config();
                    let mut lines = vec![
                        format!("💼 *Account — Binance Spot*"),
                        format!("Exchange: {}", escape_mdv2(exchange.exchange_name())),
                        format!("Mode: {}", if cfg.dry_run { "🧪 DRY RUN" } else { "🔴 LIVE" }),
                        format!("API Key: `{}`", escape_mdv2(&exchange.api_key_display())),
                        format!("{}: {:.2} free \\| {:.2} locked", stable_asset, stable_free, stable_locked),
                        format!(""),
                        format!("🏦 *BTC Treasury*"),
                        format!("BTC Holdings: {:.8}", ts.current_btc),
                        format!("BTC Vault: {:.8}", ts.btc_treasury_vault),
                        format!("Compound: {:.8}", ts.compound_balance),
                        format!("Trades: {} \\| Win: {} \\| Loss: {}", ts.total_trades, ts.winning_trades, ts.losing_trades),
                    ];

                    if !ts.trading_paused_until.is_empty() {
                        lines.push(format!("⏸️ *Paused Until:* {}", escape_mdv2(&ts.trading_paused_until)));
                    }

                    // Show other non-zero balances
                    let other: Vec<_> = balances.iter()
                        .filter(|b| b.asset != "USDT" && b.asset != "USDC" && b.asset != "BTC")
                        .filter(|b| b.free > 0.0 || b.locked > 0.0)
                        .collect();
                    if !other.is_empty() {
                        lines.push(String::new());
                        lines.push("*Other Assets:*".to_string());
                        for b in other {
                            lines.push(format!("{}: {:.8} free \\| {:.8} locked", escape_mdv2(&b.asset), b.free, b.locked));
                        }
                    }

                    // Open orders
                    if let Some(ref scanner) = self.scanner {
                        let pairs = scanner.get_pairs().await;
                        let mut all_orders: Vec<BtcAdvisoryPosition> = Vec::new();
                        for pair in &pairs {
                            if let Ok(orders) = exchange.get_open_orders(pair).await {
                                all_orders.extend(orders);
                            }
                        }
                        if !all_orders.is_empty() {
                            lines.push(String::new());
                            lines.push("*Open Orders:*".to_string());
                            for o in &all_orders {
                                lines.push(format!(
                                    "{} {}: {} @ {} \\| TP: {:.1}% \\| SL: {:.1}%",
                                    escape_mdv2(&o.side),
                                    escape_mdv2(&o.id),
                                    o.size,
                                    o.entry_price,
                                    o.take_profit_pct,
                                    o.stop_loss_pct,
                                ));
                            }
                        }
                    }

                    lines.join("\n")
                }
                Err(e) => format!("Failed: {}", e),
            }
        } else {
            "Exchange not configured. Set EXCHANGE_API_KEY and EXCHANGE_API_SECRET.".into()
        };
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_market [PAIR] — live market data + OHLCV summary.
    /// Defaults to BTCUSDT (price reference), NOT a BTC-quote pair.
    async fn cmd_market(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = args.as_deref().unwrap_or("BTCUSDT").trim().to_uppercase();
        let text = if let Some(ref exchange) = self.exchange {
            match exchange.get_market_data(&pair).await {
                Ok(data) => {
                    format!(
                        "*{} — Binance Spot*\nRegime: {}\nTrend: {:.1}\nVolume: {:.1}/10\nLiquidity: {:.1}/10\nSpread: {:.1}/10\nVolatility: {:.1}/10\nConfidence: {:.2}",
                        escape_mdv2(&pair),
                        escape_mdv2(&data.market_regime),
                        data.trend_strength,
                        data.volume_score,
                        data.liquidity_score,
                        data.spread_score,
                        data.volatility_score,
                        data.confidence,
                    )
                }
                Err(e) => format!("Failed to fetch market data for {}: {}", pair, e),
            }
        } else {
            "Exchange not configured".into()
        };
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_advisory [PAIR] — full advisory on demand.
    /// PAIR is a BTC-quote pair (e.g. SOLBTC, ETHBTC) or BTCUSDT.
    async fn cmd_advisory(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = args.as_deref().unwrap_or("BTCUSDT").trim().to_uppercase();
        let _ = bot_send_plain(bot, msg, &format!("🔍 Running advisory for {}...", pair)).await;

        let (market_data, positions) = if let Some(ref exchange) = self.exchange {
            let md = exchange.get_market_data(&pair).await.unwrap_or_else(|e| {
                tracing::error!("Market data: {}", e);
                default_market_data()
            });
            let orders = exchange.get_open_orders(&pair).await.ok().unwrap_or_default();
            (md, orders)
        } else {
            (default_market_data(), vec![])
        };

        let treasury = self.mem.get_treasury_state();
        let input = BtcAdvisoryInput {
            market_data,
            treasury,
            open_positions: positions,
            loss_streak: 0,
        };

        let advisory = self.engine.analyze(&input).await;

        let text = format!(
            "*Advisory Result — {}*\n\
            Recommendation: *{}*\n\
            Confidence: {:.2}\n\
            Risk Level: *{}*\n\
            Treasury Mode: {}\n\
            Market Regime: {}\n\
            LLM Active: {}\n\n\
            Reason: {}\n\
            Warnings: {}",
            escape_mdv2(&pair),
            escape_mdv2(&advisory.recommendation),
            advisory.confidence,
            escape_mdv2(&advisory.risk_level),
            escape_mdv2(&advisory.treasury_mode),
            escape_mdv2(&advisory.market_regime),
            advisory.bypass_quant,
            escape_mdv2(&advisory.reason),
            escape_mdv2(&advisory.warnings.join(", ")),
        );

        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_treasury — BTC treasury state, vault, compound, trade stats.
    async fn cmd_treasury(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let ts = self.mem.get_treasury_state();
        let cfg = self.mem.get_config();
        let win_rate = if ts.total_trades > 0 {
            (ts.winning_trades as f64 / ts.total_trades as f64 * 100.0)
        } else {
            0.0
        };
        let text = format!(
            "🏦 *BTC Treasury — Binance Spot*\n\n\
            BTC Holdings: {:.8}\n\
            BTC Vault: {:.8} ⚠️ *never traded*\n\
            Compound: {:.8}\n\n\
            📊 Trade Stats\n\
            Total: {} \\| Win: {} \\| Loss: {}\n\
            Win Rate: {:.1}%\n\n\
            💰 Capital\n\
            Initial: ${:.2}\n\
            Compound Split: {:.0}%\n\
            Treasury Split: {:.0}%\n\n\
            ⚙️ Risk\n\
            Max Positions: {}\n\
            Risk/Trade: {:.1}%\n\
            TP: {:.1}% \\| SL: {:.1}%\n\
            AI Threshold: {:.0}\n\n\
            Last Update: {}",
            ts.current_btc,
            ts.btc_treasury_vault,
            ts.compound_balance,
            ts.total_trades,
            ts.winning_trades,
            ts.losing_trades,
            win_rate,
            cfg.initial_capital_usdt,
            cfg.compound_pct * 100.0,
            cfg.treasury_pct * 100.0,
            cfg.max_positions,
            cfg.risk_per_trade_pct * 100.0,
            cfg.take_profit_pct,
            cfg.stop_loss_pct,
            cfg.min_score_threshold,
            escape_mdv2(&ts.last_update),
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_positions — open positions with TP/SL/trailing.
    async fn cmd_positions(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let positions = self.mem.get_positions();
        if positions.is_empty() {
            send_mdv2_safe(bot, msg.chat.id, "📭 No open positions\nCash is a position — wait for AI score ≥ 80").await?;
            return Ok(());
        }

        let mut lines = vec!["📊 *Open Positions — Binance Spot*\n".into()];
        for (i, p) in positions.iter().enumerate() {
            let trailing_icon = if p.use_trailing { "🏃" } else { "—" };
            lines.push(format!(
                "{}. *{}*\n  Entry: {} | Current: {}\n  Size: {} | PnL: {:.2}%\n  TP: {:.1}% {} | SL: {:.1}%",
                i + 1,
                escape_mdv2(&p.id),
                p.entry_price,
                p.current_price,
                p.size,
                p.pnl_btc,
                p.take_profit_pct,
                trailing_icon,
                p.stop_loss_pct,
            ));
        }
        let text = lines.join("\n");
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_scan [PAIR] — scanner stats with AI scores.
    async fn cmd_scan(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active").await?;
                return Ok(());
            }
        };

        if let Some(pair_raw) = args {
            let pair = pair_raw.trim().to_uppercase();
            let ps = match scanner.get_pair_state(&pair).await {
                Some(ps) => ps,
                None => {
                    bot_send_plain(bot, msg, &format!("Pair '{}' not found in scanner", pair)).await?;
                    return Ok(());
                }
            };
            let snapshot = ps.stats.snapshot();
            let last_time = ps.last_scan_time.read().await;
            let last_regime = ps.last_regime.read().await;
            let last_rec = ps.last_recommendation.read().await;
            let last_conf = *ps.last_confidence.read().await;
            let last_risk = ps.last_risk_level.read().await;
            let last_reason = ps.last_reason.read().await;

            let time_short = if last_time.len() > 16 {
                &last_time[11..19]
            } else if last_time.is_empty() {
                "never"
            } else {
                &*last_time
            };
            let reason_short = if last_reason.len() > 80 {
                format!("{}...", &last_reason[..77])
            } else {
                last_reason.to_string()
            };

            let score_bar = score_bar(last_conf);

            let text = format!(
                "*{} — Scanner*\n\n\
                Scans: {} \\| ✅ {} \\| 👁 {} \\| 🛡 {} \\| ❌ {} \\| ⚠️ {}\n\n\
                Last Scan: {}\n\
                Regime: {}\n\
                Recommendation: *{}*\n\
                AI Score: {}{:.2}\n\
                Risk: {}\n\
                {}",
                escape_mdv2(&pair),
                snapshot.scanned, snapshot.approve, snapshot.monitor,
                snapshot.protect, snapshot.reject, snapshot.errors,
                escape_mdv2(time_short),
                escape_mdv2(&last_regime),
                escape_mdv2(&last_rec),
                score_bar,
                last_conf,
                escape_mdv2(&last_risk),
                escape_mdv2(&reason_short),
            );
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        } else {
            let snapshots = scanner.all_snapshots().await;
            if snapshots.is_empty() {
                bot_send_plain(bot, msg, "No pairs configured\nUse /btc_addpair or /btc_discover to add pairs").await?;
                return Ok(());
            }

            let cfg = self.mem.get_config();
            let threshold = cfg.min_score_threshold;
            let mut lines = vec![format!("*Scanner — AI Scores (threshold: {:.0})*\n", threshold)];
            for s in &snapshots {
                let icon = match s.last_recommendation.as_str() {
                    "APPROVE" => "✅",
                    "MONITOR" => "👁",
                    "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => "🛡",
                    _ if s.last_recommendation.is_empty() => "⏳",
                    _ => "❌",
                };
                let score_bar = score_bar(s.last_confidence);
                lines.push(format!(
                    "{} {} — AI: {}{:.2} \\| {} \\| {}",
                    icon,
                    escape_mdv2(&s.pair),
                    score_bar,
                    s.last_confidence,
                    escape_mdv2(&s.last_recommendation),
                    escape_mdv2(&s.last_risk_level),
                ));
            }
            lines.push(String::new());
            lines.push("ℹ️ Score ≥ 80 = AMBIL POSISI | < 80 = DO NOTHING".into());
            let text = lines.join("\n");
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        }
        Ok(())
    }

    /// /btc_pairs — list active scanned BTC-quote pairs.
    async fn cmd_pairs(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active").await?;
                return Ok(());
            }
        };
        let pairs = scanner.get_pairs().await;
        if pairs.is_empty() {
            bot_send_plain(bot, msg, "No pairs configured\nUse /btc_addpair <PAIR> or /btc_discover").await?;
        } else {
            let lines: Vec<String> = pairs.iter()
                .enumerate()
                .map(|(i, p)| format!("{}. {}", i + 1, escape_mdv2(p)))
                .collect();
            let text = format!(
                "📋 *Active Pairs — Binance BTC‑Quote* ({})\n{}\n\nℹ️ Format: SYMBOLBTC\nExamples: SOLBTC, ETHBTC, SUIBTC, LINKBTC",
                pairs.len(),
                lines.join("\n"),
            );
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        }
        Ok(())
    }

    /// /btc_addpair <PAIR> — add a BTC-quote pair to the scanner.
    async fn cmd_addpair(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = match args {
            Some(ref p) if !p.trim().is_empty() => p.trim().to_uppercase(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_addpair <PAIR>\nExamples:\n  /btc_addpair SOLBTC\n  /btc_addpair ETHBTC\n  /btc_addpair SUIBTC\n\nOr use /btc_discover to auto-add all BTC-quote pairs").await?;
                return Ok(());
            }
        };

        // Validate format
        if pair.len() > 15 || !pair.chars().all(|c| c.is_ascii_alphanumeric()) {
            bot_send_plain(bot, msg, &format!("Invalid pair name: '{}'", pair)).await?;
            return Ok(());
        }

        // Must end with BTC (case-insensitive check)
        if !pair.to_uppercase().ends_with("BTC") {
            bot_send_plain(bot, msg, &format!("'{}' is not a BTC-quote pair. Use format: SYMBOLBTC\nExamples: SOLBTC, ETHBTC, DOGEBTC", pair)).await?;
            return Ok(());
        }

        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active (exchange not configured)").await?;
                return Ok(());
            }
        };

        // Validate pair exists on Binance
        if let Some(ref exchange) = self.exchange {
            match exchange.validate_symbol(&pair).await {
                Ok(true) => {}
                Ok(false) => {
                    bot_send_plain(bot, msg, &format!("Pair '{}' not found on Binance or not trading", pair)).await?;
                    return Ok(());
                }
                Err(e) => {
                    bot_send_plain(bot, msg, &format!("Failed to verify '{}': {}", pair, e)).await?;
                    return Ok(());
                }
            }
        }

        if scanner.add_pair(&pair).await {
            let pairs = scanner.get_pairs().await;
            let mut cfg = self.mem.get_config();
            cfg.scanner_pairs = pairs.clone();
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ Added '{}' to scanner\n{} pairs now active: {}", pair, pairs.len(), pairs.join(", "))).await?;
        } else {
            bot_send_plain(bot, msg, &format!("'{}' already in scanner", pair)).await?;
        }
        Ok(())
    }

    /// /btc_removepair <PAIR> — remove a pair from the scanner.
    async fn cmd_removepair(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = match args {
            Some(ref p) if !p.trim().is_empty() => p.trim().to_uppercase(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_removepair <PAIR>\nExample: /btc_removepair DOGEBTC").await?;
                return Ok(());
            }
        };

        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active").await?;
                return Ok(());
            }
        };

        if scanner.remove_pair(&pair).await {
            let pairs = scanner.get_pairs().await;
            let mut cfg = self.mem.get_config();
            cfg.scanner_pairs = pairs.clone();
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ Removed '{}'\n{} pairs remaining: {}", pair, pairs.len(), pairs.join(", "))).await?;
        } else {
            bot_send_plain(bot, msg, &format!("'{}' not found in scanner", pair)).await?;
        }
        Ok(())
    }

    /// /btc_discover — auto-discover all BTC-quote pairs on Binance.
    async fn cmd_discover(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let exchange = match &self.exchange {
            Some(e) => e.as_ref(),
            None => {
                bot_send_plain(bot, msg, "Exchange not configured").await?;
                return Ok(());
            }
        };

        let _ = bot_send_plain(bot, msg, "🔍 Discovering BTC-quote pairs on Binance...").await;

        // Use BinanceClient's discover_btc_pairs if available
        let binance = exchange.exchange_name();
        if binance != "Binance" {
            bot_send_plain(bot, msg, "Auto-discover only works with Binance").await?;
            return Ok(());
        }

        // Try to call discover_btc_pairs via downcasting
        // We do this via a helper in binance.rs; for now use validate_symbol on common pairs
        // Actually, the scanner already has discover_btc_pairs. We need to expose it.
        // For simplicity, show a list of common pairs and ask user to add them.
        let common_pairs = vec![
            "ETHBTC", "SOLBTC", "SUIBTC", "LINKBTC", "DOGEBTC",
            "ADABTC", "XRPBTC", "AVAXBTC", "DOTBTC", "MATICBTC",
            "LTCBTC", "UNI BTC", "AAVEBTC", "ATOMBTC", "FETBTC",
            "NEARBTC", "FTMBTC", "ALGO BTC", "ICP BTC", "ARBBTC",
        ];
        let text = format!(
            "*Auto-discover BTC-Quote Pairs*\n\n\
            Binance Spot has ~50 BTC-quote pairs.\n\
            Use /btc_addpair to add them one by one:\n\n\
            /btc_addpair ETHBTC\n\
            /btc_addpair SOLBTC\n\
            /btc_addpair SUIBTC\n\
            ...etc\n\n\
            Popular pairs:\n{}",
            common_pairs.iter()
                .map(|p| format!("  • {}", p))
                .collect::<Vec<_>>()
                .join("\n"),
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_pairinfo <PAIR> — detailed AI scores for one pair.
    async fn cmd_pairinfo(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = match args {
            Some(ref p) if !p.trim().is_empty() => p.trim().to_uppercase(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_pairinfo <PAIR>\nExample: /btc_pairinfo SOLBTC").await?;
                return Ok(());
            }
        };

        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active").await?;
                return Ok(());
            }
        };

        let ps = match scanner.get_pair_state(&pair).await {
            Some(ps) => ps,
            None => {
                bot_send_plain(bot, msg, &format!("Pair '{}' not found. Add it with /btc_addpair {}", pair, pair)).await?;
                return Ok(());
            }
        };

        let snapshot = ps.stats.snapshot();
        let last_time = ps.last_scan_time.read().await;
        let last_regime = ps.last_regime.read().await;
        let last_rec = ps.last_recommendation.read().await;
        let last_conf = *ps.last_confidence.read().await;
        let last_risk = ps.last_risk_level.read().await;
        let last_reason = ps.last_reason.read().await;
        let score_bar = score_bar(last_conf);
        let cfg = self.mem.get_config();

        let text = format!(
            "*{} — AI Scores*\n\n\
            *Overall:* {}{:.2} / 100\n\
            Threshold: {:.0}\n\
            Decision: *{}*\n\n\
            *Scanner Stats*\n\
            Total Scans: {}\n\
            ✅ Approve: {} | 👁 Monitor: {} | 🛡 Protect: {} | ❌ Reject: {}\n\n\
            *Last Scan ({})*\n\
            Regime: {}\n\
            Risk: {}\n\
            Reason: {}",
            escape_mdv2(&pair),
            score_bar,
            last_conf,
            cfg.min_score_threshold,
            escape_mdv2(&last_rec),
            snapshot.scanned,
            snapshot.approve,
            snapshot.monitor,
            snapshot.protect,
            snapshot.reject,
            if last_time.len() > 16 { &last_time[11..19] } else { &last_time },
            escape_mdv2(&last_regime),
            escape_mdv2(&last_risk),
            escape_mdv2(&last_reason),
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_history — recent decisions.
    async fn cmd_history(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let decisions = self.mem.get_decisions();
        if decisions.is_empty() {
            send_mdv2_safe(bot, msg.chat.id, "*No decision history yet*").await?;
            return Ok(());
        }

        let recent: Vec<_> = decisions.iter().rev().take(10).collect();
        let mut lines = vec!["*Recent Decisions — Binance Spot*\n".into()];

        for (i, d) in recent.iter().enumerate() {
            let icon = match d.advisory.recommendation.as_str() {
                "APPROVE" => "✅",
                "MONITOR" => "👁",
                "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => "🛡",
                _ => "❌",
            };
            let ts = if d.timestamp.len() > 16 {
                &d.timestamp[11..19]
            } else {
                &d.timestamp
            };
            let reason_short = if d.advisory.reason.len() > 70 {
                format!("{}...", &d.advisory.reason[..67])
            } else {
                d.advisory.reason.clone()
            };
            lines.push(format!(
                "{}{} {} {} \\- {} *{}* \\(conf: {:.2}\\)\n  \\_{}_",
                i + 1,
                r"\.",
                escape_mdv2(ts),
                icon,
                escape_mdv2(&d.advisory.market_regime),
                escape_mdv2(&d.advisory.recommendation),
                d.advisory.confidence,
                escape_mdv2(&reason_short),
            ));
        }

        let text = lines.join("\n");
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_lessons — recent lessons.
    async fn cmd_lessons(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let lessons = self.mem.get_lessons();
        if lessons.is_empty() {
            send_mdv2_safe(bot, msg.chat.id, "*No lessons yet*").await?;
            return Ok(());
        }

        let recent: Vec<_> = lessons.iter().rev().take(5).collect();
        let mut lines = vec!["*Recent Lessons*\n".into()];

        for (i, lesson) in recent.iter().enumerate() {
            let short = if lesson.len() > 120 {
                format!("{}...", &lesson[..117])
            } else {
                lesson.to_string()
            };
            lines.push(format!("{}{} {}", i + 1, r"\.", escape_mdv2(&short)));
        }

        let text = lines.join("\n");
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_skills — skill listing (static, safe).
    async fn cmd_skills(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        send_mdv2_safe(bot, msg.chat.id, SKILLS_TEXT).await?;
        Ok(())
    }

    /// /btc_config — current configuration.
    async fn cmd_config(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let cfg = self.mem.get_config();
        let ts = self.mem.get_treasury_state();
        let win_rate = if ts.total_trades > 0 {
            (ts.winning_trades as f64 / ts.total_trades as f64 * 100.0)
        } else {
            0.0
        };
        let text = format!(
            "⚙️ *Config — BTC Treasury Accumulation*\n\n\
            *Trading*\n\
            Exchange: Binance Spot\n\
            Mode: {}\n\
            Initial Capital: ${:.2}\n\
            Max Positions: {}\n\
            Risk/Trade: {:.1}%\n\n\
            *Thresholds*\n\
            AI Score Threshold: {:.0} (>= 80 = AMBIL POSISI)\n\
            Min Confidence: {:.2}\n\
            Max Exposure: {:.2}\n\n\
            *Entry/Exit*\n\
            Take Profit: {:.1}%\n\
            Stop Loss: {:.1}%\n\
            Trailing TP: {:.1}% — {}\n\n\
            *Treasury Split*\n\
            Compound: {:.0}%\n\
            BTC Vault: {:.0}%\n\n\
            *Risk Controls*\n\
            Max Consecutive Losses: {}\n\
            Daily Loss Limit: {:.8} BTC\n\
            Pause on Drawdown > 10%\n\n\
            *Scanner*\n\
            Pairs: {}\n\
            Win Rate: {:.1}%\n\
            Paused Until: {}",
            if cfg.dry_run { "🧪 DRY RUN" } else { "🔴 LIVE" },
            cfg.initial_capital_usdt,
            cfg.max_positions,
            cfg.risk_per_trade_pct * 100.0,
            cfg.min_score_threshold,
            cfg.min_confidence,
            cfg.max_exposure,
            cfg.take_profit_pct,
            cfg.stop_loss_pct,
            cfg.trailing_tp_pct,
            if cfg.use_trailing { "ON" } else { "OFF" },
            cfg.compound_pct * 100.0,
            cfg.treasury_pct * 100.0,
            cfg.max_consecutive_losses,
            cfg.daily_loss_limit_btc,
            cfg.scanner_pairs.len(),
            win_rate,
            if ts.trading_paused_until.is_empty() { "—".to_string() } else { ts.trading_paused_until.clone() },
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_setconfig <key> <value> — live config update.
    async fn cmd_setconfig(
        &self,
        bot: &Bot,
        msg: &Message,
        key: &str,
        val: &str,
    ) -> Result<(), teloxide::RequestError> {
        let mut cfg = self.mem.get_config();
        let (updated, new_val_str) = match key {
            "enabled" => {
                match val.parse::<bool>() {
                    Ok(v) => { cfg.enabled = v; (true, val.to_string()) }
                    Err(_) => {
                        bot_send_plain(bot, msg, &format!("Invalid boolean for 'enabled': '{}'. Use true or false.", val)).await?;
                        return Ok(());
                    }
                }
            }
            // New BTC accumulation config keys
            "take_profit_pct" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 && v <= 100.0 => { cfg.take_profit_pct = v; (true, format!("{:.1}%", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Use 0-100 (e.g. 5.5 for 5.5%)", val)).await?;
                        return Ok(());
                    }
                }
            }
            "stop_loss_pct" => {
                match val.parse::<f64>() {
                    Ok(v) if v <= 0.0 && v >= -100.0 => { cfg.stop_loss_pct = v; (true, format!("{:.1}%", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. SL must be negative (e.g. -1.5 for -1.5%)", val)).await?;
                        return Ok(());
                    }
                }
            }
            "trailing_tp_pct" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 => { cfg.trailing_tp_pct = v; (true, format!("{:.1}%", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Use positive number (e.g. 3.0)", val)).await?;
                        return Ok(());
                    }
                }
            }
            "use_trailing" => {
                match val.parse::<bool>() {
                    Ok(v) => { cfg.use_trailing = v; (true, val.to_string()) }
                    Err(_) => {
                        bot_send_plain(bot, msg, &format!("Invalid boolean: '{}'. Use true or false.", val)).await?;
                        return Ok(());
                    }
                }
            }
            "min_score_threshold" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 && v <= 100.0 => { cfg.min_score_threshold = v; (true, format!("{:.0}", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. AI threshold 0-100", val)).await?;
                        return Ok(());
                    }
                }
            }
            "risk_per_trade_pct" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 && v <= 100.0 => { cfg.risk_per_trade_pct = v / 100.0; (true, format!("{:.1}%", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Risk per trade 0-100 (e.g. 1.0 for 1%)", val)).await?;
                        return Ok(());
                    }
                }
            }
            "max_positions" => {
                match val.parse::<i32>() {
                    Ok(v) if v >= 0 && v <= 10 => { cfg.max_positions = v; (true, val.to_string()) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Max 0-10 positions", val)).await?;
                        return Ok(());
                    }
                }
            }
            "compound_pct" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 && v <= 100.0 => { cfg.compound_pct = v / 100.0; (true, format!("{:.0}%", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Compound % 0-100", val)).await?;
                        return Ok(());
                    }
                }
            }
            "initial_capital_usdt" => {
                match val.parse::<f64>() {
                    Ok(v) if v > 0.0 => { cfg.initial_capital_usdt = v; (true, format!("${:.2}", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Must be > 0", val)).await?;
                        return Ok(());
                    }
                }
            }
            "dry_run" => {
                match val.parse::<bool>() {
                    Ok(v) => { cfg.dry_run = v; (true, val.to_string()) }
                    Err(_) => {
                        bot_send_plain(bot, msg, &format!("Invalid boolean for 'dry_run': '{}'. Use true or false.", val)).await?;
                        return Ok(());
                    }
                }
            }
            // Legacy keys still supported
            "llm_activation_threshold" | "min_confidence" | "max_exposure" | "safe_mode_volatility" | "safe_mode_drawdown" => {
                match val.parse::<f64>() {
                    Ok(v) => {
                        let v = v.clamp(0.0, 1.0);
                        match key {
                            "llm_activation_threshold" => cfg.llm_activation_threshold = v,
                            "min_confidence" => cfg.min_confidence = v,
                            "max_exposure" => cfg.max_exposure = v,
                            "safe_mode_volatility" => cfg.safe_mode_volatility = v,
                            "safe_mode_drawdown" => cfg.safe_mode_drawdown = v,
                            _ => unreachable!(),
                        }
                        (true, format!("{:.4}", v))
                    }
                    Err(_) => {
                        bot_send_plain(bot, msg, &format!("Invalid number for '{}': '{}'", key, val)).await?;
                        return Ok(());
                    }
                }
            }
            "daily_loss_limit_btc" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 => { cfg.daily_loss_limit_btc = v; (true, format!("{:.8} BTC", v)) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Must be ≥ 0", val)).await?;
                        return Ok(());
                    }
                }
            }
            "max_consecutive_losses" => {
                match val.parse::<i32>() {
                    Ok(v) if v >= 0 => { cfg.max_consecutive_losses = v; (true, val.to_string()) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("Invalid: '{}'. Must be ≥ 0", val)).await?;
                        return Ok(());
                    }
                }
            }
            _ => {
                bot_send_plain(bot, msg, "Available keys:\n  take_profit_pct, stop_loss_pct, trailing_tp_pct, use_trailing\n  min_score_threshold, risk_per_trade_pct, max_positions\n  compound_pct, initial_capital_usdt, dry_run\n  enabled, llm_activation_threshold, min_confidence, max_exposure\n  max_consecutive_losses, daily_loss_limit_btc\n\nExample: /btc_setconfig take_profit_pct 6.0").await?;
                return Ok(());
            }
        };

        if updated {
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ {} = {}", key, new_val_str)).await?;
        }
        Ok(())
    }

    /// /btc_enable — enable LLM advisory.
    async fn cmd_enable(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut cfg = self.mem.get_config();
        cfg.enabled = true;
        self.mem.save_config(&cfg);
        send_mdv2_safe(bot, msg.chat.id, "✅ LLM advisory *ENABLED*").await?;
        Ok(())
    }

    /// /btc_disable — disable LLM advisory.
    async fn cmd_disable(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut cfg = self.mem.get_config();
        cfg.enabled = false;
        self.mem.save_config(&cfg);
        send_mdv2_safe(bot, msg.chat.id, "⏸️ LLM advisory *DISABLED*").await?;
        Ok(())
    }

    /// /btc_cancel — cancel all open orders across all pairs.
    async fn cmd_cancel(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let text = if let Some(ref exchange) = self.exchange {
            match self.scanner {
                Some(ref scanner) => {
                    let pairs = scanner.get_pairs().await;
                    let mut total = 0;
                    for pair in &pairs {
                        match exchange.cancel_all(pair).await {
                            Ok(results) => total += results.len(),
                            Err(e) => {
                                tracing::error!("Failed to cancel orders for {}: {}", pair, e);
                            }
                        }
                    }
                    format!("✅ Cancelled {} open orders across {} pairs", total, pairs.len())
                }
                None => {
                    match exchange.cancel_all("BTCUSDT").await {
                        Ok(results) => format!("✅ Cancelled {} open orders", results.len()),
                        Err(e) => format!("Failed: {}", e),
                    }
                }
            }
        } else {
            "Exchange not configured".into()
        };
        bot_send_plain(bot, msg, &text).await?;
        Ok(())
    }

    /// /btc_buy <SIZE> <PAIR> — place market buy on Binance Spot with dynamic TP/SL.
    /// PAIR is a BTC-quote pair (e.g. SOLBTC) or BTCUSDT.
    async fn cmd_buy(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let exchange = match &self.exchange {
            Some(e) => e,
            None => {
                bot_send_plain(bot, msg, "Exchange not configured").await?;
                return Ok(());
            }
        };

        let (size_str, pair) = match args {
            Some(ref a) => {
                let parts: Vec<&str> = a.split_whitespace().collect();
                if parts.is_empty() {
                    bot_send_plain(bot, msg, "Usage: /btc_buy <SIZE> <PAIR>\nExamples:\n  /btc_buy 100 SOLBTC\n  /btc_buy 0.5 ETHBTC\n  /btc_buy 10 BTCUSDT").await?;
                    return Ok(());
                }
                let size = parts[0].to_string();
                let pair = parts.get(1).map(|s| s.to_uppercase()).unwrap_or_else(|| "BTCUSDT".to_string());
                (size, pair)
            }
            None => {
                bot_send_plain(bot, msg, "Usage: /btc_buy <SIZE> <PAIR>\nExamples:\n  /btc_buy 100 SOLBTC\n  /btc_buy 0.5 ETHBTC").await?;
                return Ok(());
            }
        };

        let size: f64 = match size_str.parse() {
            Ok(s) if s > 0.0 => s,
            _ => {
                bot_send_plain(bot, msg, &format!("Invalid size: '{}'. Must be a positive number.", size_str)).await?;
                return Ok(());
            }
        };

        let _ = bot_send_plain(bot, msg, &format!("📈 Placing BUY order on Binance Spot...\n{} {} @ market price...", size, pair)).await;

        let cfg = self.mem.get_config();
        let ts = self.mem.get_treasury_state();

        // Check trading pause
        if !ts.trading_paused_until.is_empty() {
            if let Ok(paused) = chrono::DateTime::parse_from_rfc3339(&ts.trading_paused_until) {
                if chrono::Utc::now() < paused {
                    bot_send_plain(bot, msg, &format!("⏸️ Trading is PAUSED until {}\nUse /btc_resume to resume.", paused.format("%Y-%m-%d %H:%M UTC"))).await?;
                    return Ok(());
                }
            }
        }

        // Dry run mode
        if cfg.dry_run {
            let advisory = self.engine.analyze(&BtcAdvisoryInput {
                market_data: exchange.get_market_data(&pair).await.unwrap_or_else(|_| default_market_data()),
                treasury: ts,
                open_positions: self.mem.get_positions(),
                loss_streak: 0,
            }).await;
            let current_price = exchange.get_current_price(&pair).await.unwrap_or(0.0);
            record_position_from_advisory(&*self.mem, &advisory, current_price, size, &pair, "buy");
            send_mdv2_safe(
                bot, msg.chat.id,
                &format!("🧪 *DRY RUN — Simulated Buy*\nPair: {}\nSize: {}\nTP: {:.1}% | SL: {:.1}%\nReason: {}", escape_mdv2(&pair), size, advisory.dynamic_take_profit, advisory.dynamic_stop_loss, escape_mdv2(&advisory.tp_reason))
            ).await?;
            return Ok(());
        }

        // Run advisory to get LLM dynamic TP/SL
        let market_data = match exchange.get_market_data(&pair).await {
            Ok(m) => m,
            Err(e) => {
                bot_send_plain(bot, msg, &format!("Failed to fetch market data: {}", e)).await?;
                return Ok(());
            }
        };

        let treasury = self.mem.get_treasury_state();
        let positions = self.mem.get_positions();
        let loss_streak = {
            let mut streak = 0;
            for pos in positions.iter().rev() {
                if pos.pnl_btc < 0.0 { streak += 1; } else { break; }
            }
            streak
        };

        let input = BtcAdvisoryInput {
            market_data: market_data.clone(),
            treasury,
            open_positions: positions,
            loss_streak,
        };

        let advisory = self.engine.analyze(&input).await;
        let current_price = match exchange.get_current_price(&pair).await {
            Ok(p) => p,
            Err(_) => 0.0,
        };

        // Place market buy on Binance
        match exchange.place_market_buy(&pair, size).await {
            Ok(result) => {
                let text = format!(
                    "✅ *Order Placed — Binance Spot*\n\
                    Pair: {}\n\
                    Side: BUY\n\
                    Size: {}\n\
                    Order ID: {}\n\
                    Status: {}\n\n\
                    *Dynamic TP/SL from LLM:*\n\
                    Take Profit: {:.1}% — {}\n\
                    Stop Loss: {:.1}% — {}",
                    pair,
                    size,
                    result.order_id,
                    result.status,
                    advisory.dynamic_take_profit,
                    advisory.tp_reason,
                    advisory.dynamic_stop_loss,
                    advisory.sl_reason,
                );

                if result.status == "filled" || result.status == "new" {
                    record_position_from_advisory(
                        &*self.mem,
                        &advisory,
                        current_price,
                        size,
                        &pair,
                        "buy",
                    );
                }

                send_mdv2_safe(bot, msg.chat.id, &text).await?;
            }
            Err(e) => {
                bot_send_plain(bot, msg, &format!("Order failed: {}", e)).await?;
            }
        }

        Ok(())
    }

    /// /btc_sell — close all open positions with market sell on Binance Spot.
    async fn cmd_sell(&self, bot: &Bot, msg: &Message, _args: Option<String>) -> Result<(), teloxide::RequestError> {
        let exchange = match &self.exchange {
            Some(e) => e,
            None => {
                bot_send_plain(bot, msg, "Exchange not configured").await?;
                return Ok(());
            }
        };

        let cfg = self.mem.get_config();
        let positions = self.mem.get_positions();
        if positions.is_empty() {
            bot_send_plain(bot, msg, "No open positions to close").await?;
            return Ok(());
        }

        if cfg.dry_run {
            let mut results: Vec<String> = Vec::new();
            for pos in &positions {
                self.mem.update_treasury_on_close(&pos.id, pos.pnl_btc, pos.entry_price * pos.size);
                let lesson = format!(
                    "[BTC][MANUAL][DRY RUN] {}: PnL {:.2}%. Size: {}. Manual close via /btc_sell.",
                    pos.id, pos.pnl_btc, pos.size
                );
                self.mem.add_lesson(lesson);
                results.push(format!("{} — PnL: {:.2}%", escape_mdv2(&pos.id), pos.pnl_btc));
            }
            self.mem.save_positions(&[]);
            let text = format!("🧪 *DRY RUN — Simulated Close All*\n\n{}", results.join("\n"));
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
            return Ok(());
        }

        let mut results: Vec<String> = Vec::new();
        for pos in &positions {
            let pair = &pos.id;
            let size = pos.size;

            // Cancel any open orders first
            let _ = exchange.cancel_all(pair).await;

            match exchange.place_market_sell(pair, size).await {
                Ok(result) => {
                    let position_value_usdt = pos.entry_price * size;
                    self.mem.update_treasury_on_close(pair, pos.pnl_btc, position_value_usdt);

                    let lesson = format!(
                        "[BTC][MANUAL] {}: PnL {:.2}%. Size: {}. Manual close via /btc_sell. TP: {:.1}%, SL: {:.1}%",
                        pair, pos.pnl_btc, size, pos.take_profit_pct, pos.stop_loss_pct
                    );
                    self.mem.add_lesson(lesson);

                    results.push(format!(
                        "✅ {} closed — {} @ {} | PnL: {:.2}%",
                        pair, size, result.order_id, pos.pnl_btc
                    ));
                }
                Err(e) => {
                    results.push(format!("❌ {} failed: {}", pair, e));
                }
            }
        }

        // Remove all closed positions
        self.mem.save_positions(&[]);

        let text = format!("*Close Results — Binance Spot*\n\n{}", results.join("\n"));
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_close <index> — close position by index (1-based).
    async fn cmd_close(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let exchange = match &self.exchange {
            Some(e) => e,
            None => {
                bot_send_plain(bot, msg, "Exchange not configured").await?;
                return Ok(());
            }
        };

        let idx_str = match args {
            Some(ref s) if !s.trim().is_empty() => s.trim().to_string(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_close <index>\nExample: /btc_close 1\n\nUse /btc_positions to see indices.").await?;
                return Ok(());
            }
        };

        let idx: usize = match idx_str.parse::<usize>() {
            Ok(i) if i >= 1 => i - 1,
            _ => {
                bot_send_plain(bot, msg, &format!("Invalid index: '{}'. Must be a positive number.", idx_str)).await?;
                return Ok(());
            }
        };

        let mut positions = self.mem.get_positions();
        if idx >= positions.len() {
            bot_send_plain(bot, msg, &format!("Position #{} not found. You have {} open positions.", idx + 1, positions.len())).await?;
            return Ok(());
        }

        let pos = &positions[idx];
        let pair = pos.id.clone();
        let size = pos.size;
        let entry_price = pos.entry_price;
        let pnl_pct = pos.pnl_btc;

        let cfg = self.mem.get_config();
        if cfg.dry_run {
            self.mem.update_treasury_on_close(&pair, pnl_pct, entry_price * size);
            let lesson = format!(
                "[BTC][MANUAL][DRY RUN] {}: PnL {:.2}%. Size: {}. Manual close via /btc_close. TP: {:.1}%, SL: {:.1}%",
                pair, pnl_pct, size, pos.take_profit_pct, pos.stop_loss_pct
            );
            self.mem.add_lesson(lesson);
            positions.remove(idx);
            self.mem.save_positions(&positions);
            send_mdv2_safe(
                bot, msg.chat.id,
                &format!("🧪 *DRY RUN* — Simulated close\n✅ #{} {} — size: {} | PnL: {:.2}%", idx + 1, escape_mdv2(&pair), size, pnl_pct)
            ).await?;
            return Ok(());
        }

        let _ = exchange.cancel_all(&pair).await;
        match exchange.place_market_sell(&pair, size).await {
            Ok(result) => {
                self.mem.update_treasury_on_close(&pair, pnl_pct, entry_price * size);
                let lesson = format!(
                    "[BTC][MANUAL] {}: PnL {:.2}%. Size: {}. Manual close via /btc_close. TP: {:.1}%, SL: {:.1}%",
                    pair, pnl_pct, size, pos.take_profit_pct, pos.stop_loss_pct
                );
                self.mem.add_lesson(lesson);
                positions.remove(idx);
                self.mem.save_positions(&positions);
                send_mdv2_safe(
                    bot, msg.chat.id,
                    &format!("✅ #{} {} closed — {} | PnL: {:.2}%", idx + 1, escape_mdv2(&pair), result.order_id, pnl_pct)
                ).await?;
            }
            Err(e) => {
                bot_send_plain(bot, msg, &format!("❌ Failed to close #{} {}: {}", idx + 1, pair, e)).await?;
            }
        }
        Ok(())
    }

    /// /btc_closeall — force close all positions.
    async fn cmd_closeall(&self, bot: &Bot, msg: &Message, _args: Option<String>) -> Result<(), teloxide::RequestError> {
        let exchange = match &self.exchange {
            Some(e) => e,
            None => {
                bot_send_plain(bot, msg, "Exchange not configured").await?;
                return Ok(());
            }
        };

        let positions = self.mem.get_positions();
        if positions.is_empty() {
            bot_send_plain(bot, msg, "No open positions to close").await?;
            return Ok(());
        }

        let cfg = self.mem.get_config();
        let mut results: Vec<String> = Vec::new();

        if cfg.dry_run {
            for pos in &positions {
                self.mem.update_treasury_on_close(&pos.id, pos.pnl_btc, pos.entry_price * pos.size);
                let lesson = format!(
                    "[BTC][MANUAL][DRY RUN] {}: PnL {:.2}%. Size: {}. Force closeall.",
                    pos.id, pos.pnl_btc, pos.size
                );
                self.mem.add_lesson(lesson);
                results.push(format!("🧪 {} — DRY RUN close | PnL: {:.2}%", escape_mdv2(&pos.id), pos.pnl_btc));
            }
            self.mem.save_positions(&[]);
            let text = format!("🧪 *DRY RUN — Force Close All*\n\n{}", results.join("\n"));
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
            return Ok(());
        }

        for pos in &positions {
            let pair = &pos.id;
            let size = pos.size;
            let _ = exchange.cancel_all(pair).await;
            match exchange.place_market_sell(pair, size).await {
                Ok(result) => {
                    self.mem.update_treasury_on_close(pair, pos.pnl_btc, pos.entry_price * size);
                    let lesson = format!(
                        "[BTC][MANUAL] {}: PnL {:.2}%. Size: {}. Force closeall.",
                        pair, pos.pnl_btc, size
                    );
                    self.mem.add_lesson(lesson);
                    results.push(format!("✅ {} closed — {} | PnL: {:.2}%", escape_mdv2(pair), result.order_id, pos.pnl_btc));
                }
                Err(e) => {
                    results.push(format!("❌ {} failed: {}", escape_mdv2(pair), e));
                }
            }
        }
        self.mem.save_positions(&[]);
        let text = format!("*Force Close All — Binance Spot*\n\n{}", results.join("\n"));
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_dryrun on|off — toggle dry run mode.
    async fn cmd_dryrun(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let arg = match args {
            Some(ref s) => s.trim().to_lowercase(),
            None => {
                let cfg = self.mem.get_config();
                let current = if cfg.dry_run { "ON 🧪 (simulation)" } else { "OFF 🔴 (LIVE)" };
                bot_send_plain(bot, msg, &format!("Dry Run is currently: {}\n\nUse:\n  /btc_dryrun on  — enable simulation\n  /btc_dryrun off — enable live trading", current)).await?;
                return Ok(());
            }
        };

        match arg.as_str() {
            "on" => {
                let mut cfg = self.mem.get_config();
                cfg.dry_run = true;
                self.mem.save_config(&cfg);
                send_mdv2_safe(bot, msg.chat.id, "🧪 *DRY RUN enabled*\nAll trades will be simulated. No real orders on Binance.").await?;
            }
            "off" => {
                let mut cfg = self.mem.get_config();
                cfg.dry_run = false;
                self.mem.save_config(&cfg);
                send_mdv2_safe(bot, msg.chat.id, "🔴 *LIVE TRADING enabled*\n⚠️ All orders WILL execute on Binance Spot!").await?;
            }
            _ => {
                bot_send_plain(bot, msg, &format!("Invalid: '{}'. Use 'on' or 'off'.\n\n/btc_dryrun on  — simulation\n/btc_dryrun off — live trading", arg)).await?;
            }
        }
        Ok(())
    }

    /// /btc_pause — pause trading for 24 hours.
    async fn cmd_pause(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut ts = self.mem.get_treasury_state();
        let paused_until = chrono::Utc::now() + chrono::Duration::hours(24);
        ts.trading_paused_until = paused_until.to_rfc3339();
        self.mem.save_treasury_state(ts);
        send_mdv2_safe(
            bot, msg.chat.id,
            &format!("⏸️ *Trading PAUSED*\nResumes: {}\n\nAll buy/sell commands and auto-execution are blocked until then.", escape_mdv2(&paused_until.format("%Y-%m-%d %H:%M UTC").to_string()))
        ).await?;
        Ok(())
    }

    /// /btc_resume — resume trading (clear pause).
    async fn cmd_resume(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut ts = self.mem.get_treasury_state();
        ts.trading_paused_until = String::new();
        self.mem.save_treasury_state(ts);
        send_mdv2_safe(bot, msg.chat.id, "▶️ *Trading RESUMED*\nAll commands and auto-execution are now active.").await?;
        Ok(())
    }
}

// ── Helpers ──────────────────────────────────────────────────────────────────

fn default_market_data() -> BtcMarketData {
    BtcMarketData {
        pair: "BTCUSDT".into(),
        market_regime: String::new(),
        trend_strength: 0.0,
        volume_score: 5.0,
        liquidity_score: 5.0,
        spread_score: 5.0,
        volatility_score: 5.0,
        breakout_probability: 0.3,
        reversal_probability: 0.2,
        confidence: 0.5,
        active_strategy: "spot_accumulation".into(),
        portfolio_exposure: 0.0,
        daily_drawdown: 0.0,
    }
}

/// Returns a Telegram-safe visual bar for scores 0-100.
fn score_bar(score: f64) -> String {
    let filled = (score / 10.0).round() as usize;
    let empty = 10 - filled;
    let bar = "█".repeat(filled) + &"░".repeat(empty);
    format!("[{}] ", bar)
}
