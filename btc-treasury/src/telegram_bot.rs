use std::sync::Arc;

use anyhow::Result;
use teloxide::prelude::*;

use crate::engine::AdvisoryEngine;
use crate::exchange::{ExchangeClient, ExchangeOrderResult};
use crate::format::{bot_send_plain, escape_mdv2, send_mdv2_safe};
use crate::memory::MemoryStore;
use crate::models::*;
use crate::scanner::ScannerState;

// ── Static help / skills text ────────────────────────────────────────────────
// These are entirely static = no dynamic interpolation → safe for MarkdownV2.

const HELP_TEXT: &str = r#"🤖 *BTC Treasury Advisor \(Spot\)*

*Market & Advisory*
/btc\_status — Spot account balance \(BTC \+ USDT\), open orders
/btc\_market \[PAIR\] — Live orderbook \+ market regime
/btc\_advisory \[PAIR\] — Full advisory \(quant \+ LLM\)
/btc\_treasury — Treasury state
/btc\_positions — Open orders

*Scanner & History*
/btc\_scan \[PAIR\] — Scan stats & status
/btc\_history — Recent decision history
/btc\_lessons — Recent self\-learning lessons
/btc\_pairs — List active scanned pairs
/btc\_addpair \<PAIR\> — Add pair to scanner
/btc\_removepair \<PAIR\> — Remove pair

*Configuration*
/btc\_config — Current config
/btc\_setconfig \<key\> \<value\> — Update config
/btc\_enable — Enable LLM advisory
/btc\_disable — Disable LLM advisory

*Trading \(Spot\)*
/btc\_cancel — Cancel all open spot orders

*Info*
/btc\_skills — Bot skills & capabilities
/help — This message"#;

const SKILLS_TEXT: &str = r#"*BTC Treasury Advisor — Skills*

*1\. Autonomous BTC Spot Scanner*
Polls Binance spot orderbook every 30s
Derives regime, trend, liquidity, spread, volatility
Runs quant \+ LLM advisory engine
Logs every decision to btc\-decision\-log\.json

*2\. Market Regime Detection*
11 regimes: TRENDING\_BULLISH, TRENDING\_BEARISH,
RANGING, CHOPPY, BREAKOUT\_EXPANSION,
FAKE\_BREAKOUT, ACCUMULATION, DISTRIBUTION,
PANIC\_SELLOFF, LOW\_LIQUIDITY\_DANGER,
HIGH\_VOLATILITY\_DANGER

*3\. Risk Assessment Engine*
Multi\-factor: liquidity, spread, volatility,
drawdown, loss streak, confidence
Risk levels: LOW, MEDIUM, HIGH, CRITICAL

*4\. Treasury Protection*
Modes: ACCUMULATE, PROTECT, REDUCE\_RISK, SAFE\_MODE
Auto\-activates during dangerous conditions

*5\. LLM AI Reasoning*
Activated when confidence \< threshold or
dangerous conditions detected
Fallback to quant\-only when LLM fails

*6\. Self\-Learning*
Non\-approved decisions become lessons
Lessons feed LLM context for future decisions

*7\. Periodic Report*
Auto\-report every 5 minutes to configured chats
Shows scan stats, decisions, new lessons"#;

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

    async fn run_bot(&self) -> Result<()> {
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
            // Support both /btc_status and /btcstatus (underscore-stripped)
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
            "btccancel" => self.cmd_cancel(bot, msg).await,
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

    /// /btc_status or /btcstatus — account with BTC + USDT/USDC balance, open orders.
    async fn cmd_status(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let text = if let Some(ref exchange) = self.exchange {
            match exchange.get_balances().await {
                Ok(balances) => {
                    let btc_bal = balances.iter().find(|b| b.asset == "BTC");
                    let stable_bal = balances.iter().find(|b| b.asset == "USDT" || b.asset == "USDC");
                    let btc_free = btc_bal.map(|b| b.free).unwrap_or(0.0);
                    let btc_locked = btc_bal.map(|b| b.locked).unwrap_or(0.0);
                    let stable_free = stable_bal.map(|b| b.free).unwrap_or(0.0);
                    let stable_locked = stable_bal.map(|b| b.locked).unwrap_or(0.0);
                    let stable_asset = if stable_bal.is_some() {
                        if balances.iter().any(|b| b.asset == "USDC") { "USDC" } else { "USDT" }
                    } else { "USDT/USDC" };

                    let mut lines = vec![
                        format!("💼 *Account*"),
                        format!("Exchange: {}", escape_mdv2(exchange.exchange_name())),
                        format!("API Key: `{}`", escape_mdv2(&exchange.api_key_display())),
                        format!("BTC: {:.8} free \\| {:.8} locked", btc_free, btc_locked),
                        format!("{}: {:.2} free \\| {:.2} locked", stable_asset, stable_free, stable_locked),
                    ];

                    // Show other non-zero balances
                    let other: Vec<_> = balances.iter()
                        .filter(|b| b.asset != "BTC" && b.asset != "USDT" && b.asset != "USDC")
                        .filter(|b| b.free > 0.0 || b.locked > 0.0)
                        .collect();
                    if !other.is_empty() {
                        lines.push(String::new());
                        for b in other {
                            lines.push(format!("{}: {:.8} free \\| {:.8} locked", escape_mdv2(&b.asset), b.free, b.locked));
                        }
                    }

                    // Open orders across all pairs
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
                            lines.push("*Open Orders*".to_string());
                            for o in &all_orders {
                                lines.push(format!(
                                    "{} {}: {} @ {} \\| filled {:.0}%",
                                    escape_mdv2(&o.side),
                                    escape_mdv2(&o.id),
                                    o.size,
                                    o.entry_price,
                                    if o.size > 0.0 { 0.0 } else { 100.0 },
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
        send_mdv2_safe(bot, msg.chat.id,&text).await?;
        Ok(())
    }

    /// /btc_market or /btcmarket [PAIR] — live market data.
    async fn cmd_market(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = args.as_deref().unwrap_or("BTCUSDT").trim().to_uppercase();
        let text = if let Some(ref exchange) = self.exchange {
            match exchange.get_market_data(&pair).await {
                Ok(data) => {
                    format!(
                        "*{} Spot Market*\nRegime: {}\nTrend: {:.1}\nVolume: {:.1}/10\nLiquidity: {:.1}/10\nSpread: {:.1}/10\nVolatility: {:.1}/10\nConfidence: {:.2}",
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
                Err(e) => format!("Failed: {}", e),
            }
        } else {
            "Exchange not configured".into()
        };
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_advisory or /btcadvisory [PAIR]— full advisory on demand.
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
            "*Advisory Result*\n\
            Recommendation: *{}*\n\
            Confidence: {:.2}\n\
            Risk Level: *{}*\n\
            Treasury Mode: {}\n\
            Market Regime: {}\n\
            Opportunity Score: {:.0}\n\
            LLM Active: {}\n\n\
            Reason: {}\n\
            Warnings: {}",
            escape_mdv2(&advisory.recommendation),
            advisory.confidence,
            escape_mdv2(&advisory.risk_level),
            escape_mdv2(&advisory.treasury_mode),
            escape_mdv2(&advisory.market_regime),
            advisory.opportunity_score,
            advisory.bypass_quant,
            escape_mdv2(&advisory.reason),
            escape_mdv2(&advisory.warnings.join(", ")),
        );

        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_treasury or /btctreasury — treasury state.
    async fn cmd_treasury(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let ts = self.mem.get_treasury_state();
        let text = format!(
            "🏦 *Treasury*\nBTC: {:.8}\nUSDT: {:.2}\n7d Growth: {:.4}%\nLast Update: {}",
            ts.current_btc,
            ts.usdt_balance,
            ts.btc_growth_7d * 100.0,
            escape_mdv2(&ts.last_update),
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_positions or /btcpositions — open spot orders.
    async fn cmd_positions(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let text = if let Some(ref exchange) = self.exchange {
            match self.scanner {
                Some(ref scanner) => {
                    let pairs = scanner.get_pairs().await;
                    let mut all_orders: Vec<BtcAdvisoryPosition> = Vec::new();
                    for pair in &pairs {
                        if let Ok(orders) = exchange.get_open_orders(pair).await {
                            all_orders.extend(orders);
                        }
                    }
                    if all_orders.is_empty() {
                        "No open orders".into()
                    } else {
                        let lines: Vec<String> = all_orders
                            .iter()
                            .map(|o| {
                                format!(
                                    "{} {}: {} @ {} \\| filled {:.0}%",
                                    escape_mdv2(&o.side),
                                    escape_mdv2(&o.id),
                                    o.size,
                                    o.entry_price,
                                    if o.size > 0.0 { 0.0 } else { 100.0 },
                                )
                            })
                            .collect();
                        format!("📊 *Open Orders*\n{}", lines.join("\n"))
                    }
                }
                None => "Scanner not active".into(),
            }
        } else {
            "Exchange not configured".into()
        };
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_scan or /btcscan [PAIR] — scanner stats.
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

            let text = format!(
                "*{} Scanner*\n\nScanned: {}\n✅ {}\n👁 {}\n🛡 {}\n❌ {}\n⚠️ {}\n\nLast: {}\nRegime: {}\nRec: *{}*\nConf: {:.2}\nRisk: {}\n{}",
                escape_mdv2(&pair),
                snapshot.scanned, snapshot.approve, snapshot.monitor,
                snapshot.protect, snapshot.reject, snapshot.errors,
                escape_mdv2(time_short),
                escape_mdv2(&last_regime),
                escape_mdv2(&last_rec),
                last_conf,
                escape_mdv2(&last_risk),
                escape_mdv2(&reason_short),
            );
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        } else {
            let snapshots = scanner.all_snapshots().await;
            if snapshots.is_empty() {
                bot_send_plain(bot, msg, "No pairs configured").await?;
                return Ok(());
            }

            let mut lines = vec!["*Scanner Status*\n".into()];
            for s in &snapshots {
                let icon = match s.last_recommendation.as_str() {
                    "APPROVE" => "✅",
                    "MONITOR" => "👁",
                    "PROTECT_TREASURY" | "ENABLE_SAFE_MODE" | "REDUCE_EXPOSURE" => "🛡",
                    _ if s.last_recommendation.is_empty() => "⏳",
                    _ => "❌",
                };
                let time_short = if s.last_scan_time.len() > 16 {
                    &s.last_scan_time[11..19]
                } else if s.last_scan_time.is_empty() {
                    "never"
                } else {
                    &*s.last_scan_time
                };
                lines.push(format!(
                    "{} {} {} scans\\|{} {} \\(conf:{}\\)",
                    icon,
                    escape_mdv2(&s.pair),
                    s.stats.scanned,
                    time_short,
                    escape_mdv2(&s.last_recommendation),
                    s.last_confidence,
                ));
            }
            let text = lines.join("\n");
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        }
        Ok(())
    }

    /// /btc_pairs or /btcpairs — list active pairs.
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
            bot_send_plain(bot, msg, "No pairs configured").await?;
        } else {
            let text = format!("📋 *Active Pairs* ({})\n{}", pairs.len(), pairs.join("\n"));
            send_mdv2_safe(bot, msg.chat.id, &text).await?;
        }
        Ok(())
    }

    /// /btc_addpair or /btcaddpair <PAIR> — add pair to scanner.
    async fn cmd_addpair(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = match args {
            Some(ref p) if !p.trim().is_empty() => p.trim().to_uppercase(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_addpair <PAIR>\nExample: /btc_addpair ETHUSDT").await?;
                return Ok(());
            }
        };

        if pair.len() > 15 || !pair.chars().all(|c| c.is_ascii_alphanumeric()) {
            bot_send_plain(bot, msg, &format!("Invalid pair name: '{}'. Use alphanumeric (e.g. BTCUSDT).", pair)).await?;
            return Ok(());
        }

        let scanner = match self.scanner {
            Some(ref s) => s,
            None => {
                bot_send_plain(bot, msg, "Scanner not active (exchange not configured)").await?;
                return Ok(());
            }
        };

        // Validate pair exists on exchange
        if let Some(ref exchange) = self.exchange {
            match exchange.validate_symbol(&pair).await {
                Ok(true) => {}
                Ok(false) => {
                    bot_send_plain(bot, msg, &format!("Pair '{}' not found on exchange or not trading", pair)).await?;
                    return Ok(());
                }
                Err(e) => {
                    bot_send_plain(bot, msg, &format!("Failed to verify pair '{}': {}", pair, e)).await?;
                    return Ok(());
                }
            }
        }

        if scanner.add_pair(&pair).await {
            let pairs = scanner.get_pairs().await;
            let mut cfg = self.mem.get_config();
            cfg.scanner_pairs = pairs;
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ Added pair '{}' to scanner", pair)).await?;
        } else {
            bot_send_plain(bot, msg, &format!("Pair '{}' already exists in scanner", pair)).await?;
        }
        Ok(())
    }

    /// /btc_removepair or /btcremovepair <PAIR> — remove pair from scanner.
    async fn cmd_removepair(&self, bot: &Bot, msg: &Message, args: Option<String>) -> Result<(), teloxide::RequestError> {
        let pair = match args {
            Some(ref p) if !p.trim().is_empty() => p.trim().to_uppercase(),
            _ => {
                bot_send_plain(bot, msg, "Usage: /btc_removepair <PAIR>\nExample: /btc_removepair ETH").await?;
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
            cfg.scanner_pairs = pairs;
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ Removed pair '{}' from scanner", pair)).await?;
        } else {
            bot_send_plain(bot, msg, &format!("Pair '{}' not found in scanner", pair)).await?;
        }
        Ok(())
    }

    /// /btc_history or /btchistory — recent decisions.
    async fn cmd_history(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let decisions = self.mem.get_decisions();
        if decisions.is_empty() {
            send_mdv2_safe(bot, msg.chat.id, "*No decision history yet*").await?;
            return Ok(());
        }

        let recent: Vec<_> = decisions.iter().rev().take(10).collect();
        let mut lines = vec!["*Recent Decisions*\n".into()];

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
                "{}{} {} \\- {} {} *{}* \\(conf: {:.2}\\)\n  \\_{}_",
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

    /// /btc_lessons or /btclessons — recent lessons.
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

    /// /btc_skills or /btcskills — skill listing (static, safe).
    async fn cmd_skills(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        send_mdv2_safe(bot, msg.chat.id, SKILLS_TEXT).await?;
        Ok(())
    }

    /// /btc_config or /btcconfig — current configuration.
    async fn cmd_config(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let cfg = self.mem.get_config();
        let text = format!(
            "⚙️ *Config*\n\
            Enabled: {}\n\
            LLM Threshold: {:.2}\n\
            Min Confidence: {:.2}\n\
            Max Exposure: {:.2}\n\
            Daily Loss Limit: {:.8} BTC\n\
            Max Consecutive Losses: {}\n\
            Safe Mode Vol: {:.1}\n\
            Safe Mode DD: {:.2}",
            cfg.enabled,
            cfg.llm_activation_threshold,
            cfg.min_confidence,
            cfg.max_exposure,
            cfg.daily_loss_limit_btc,
            cfg.max_consecutive_losses,
            cfg.safe_mode_volatility,
            cfg.safe_mode_drawdown,
        );
        send_mdv2_safe(bot, msg.chat.id, &text).await?;
        Ok(())
    }

    /// /btc_setconfig or /btcsetconfig <key> <value> — live config update.
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
                        bot_send_plain(bot, msg, &format!("❌ Invalid boolean for 'enabled': '{}'. Use true or false.", val)).await?;
                        return Ok(());
                    }
                }
            }
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
                        bot_send_plain(bot, msg, &format!("❌ Invalid number for '{}': '{}'. Use a decimal (0.0-1.0)", key, val)).await?;
                        return Ok(());
                    }
                }
            }
            "daily_loss_limit_btc" => {
                match val.parse::<f64>() {
                    Ok(v) if v >= 0.0 => { cfg.daily_loss_limit_btc = v; (true, val.to_string()) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("❌ Invalid value for 'daily_loss_limit_btc': '{}'. Must be >= 0.", val)).await?;
                        return Ok(());
                    }
                }
            }
            "max_consecutive_losses" => {
                match val.parse::<i32>() {
                    Ok(v) if v >= 0 => { cfg.max_consecutive_losses = v; (true, val.to_string()) }
                    _ => {
                        bot_send_plain(bot, msg, &format!("❌ Invalid integer for 'max_consecutive_losses': '{}'. Must be >= 0.", val)).await?;
                        return Ok(());
                    }
                }
            }
            _ => {
                bot_send_plain(bot, msg, &format!("❌ Unknown key: '{}'. Available: enabled, llm_activation_threshold, min_confidence, max_exposure, daily_loss_limit_btc, max_consecutive_losses, safe_mode_volatility, safe_mode_drawdown", key)).await?;
                return Ok(());
            }
        };

        if updated {
            self.mem.save_config(&cfg);
            bot_send_plain(bot, msg, &format!("✅ {} = {}", key, new_val_str)).await?;
        }
        Ok(())
    }

    /// /btc_enable or /btcenable — enable LLM advisory.
    async fn cmd_enable(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut cfg = self.mem.get_config();
        cfg.enabled = true;
        self.mem.save_config(&cfg);
        send_mdv2_safe(bot, msg.chat.id, "✅ LLM advisory *ENABLED*").await?;
        Ok(())
    }

    /// /btc_disable or /btcdisable — disable LLM advisory.
    async fn cmd_disable(&self, bot: &Bot, msg: &Message) -> Result<(), teloxide::RequestError> {
        let mut cfg = self.mem.get_config();
        cfg.enabled = false;
        self.mem.save_config(&cfg);
        send_mdv2_safe(bot, msg.chat.id, "⏸️ LLM advisory *DISABLED*").await?;
        Ok(())
    }

    /// /btc_cancel or /btccancel — cancel all open orders across all pairs.
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
                    // Try BTCUSDT as fallback
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
