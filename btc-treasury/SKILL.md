# BTC Treasury Advisor — Skills

## 1. Autonomous BTC Scanner
- Polls Hyperliquid L2 orderbook every 30 seconds (configurable via `BTC_SCANNER_INTERVAL_SECS`)
- Derives market regime, trend, liquidity, spread, volatility from live orderbook depth
- Runs quant + LLM advisory engine on each scan
- Logs every decision to `btc-decision-log.json`
- Generates self-learning lessons from non-APPROVE decisions

## 2. Market Regime Detection
11 regimes classified from orderbook metrics:
- TRENDING_BULLISH, TRENDING_BEARISH
- RANGING, CHOPPY
- BREAKOUT_EXPANSION, FAKE_BREAKOUT
- ACCUMULATION, DISTRIBUTION
- PANIC_SELLOFF
- LOW_LIQUIDITY_DANGER, HIGH_VOLATILITY_DANGER

## 3. Risk Assessment Engine
Multi-factor risk scoring (0-10 scale):
- Liquidity depth, spread width, volatility
- Daily drawdown %, consecutive loss streak
- Signal confidence, reversal probability
- Portfolio exposure %
- Treasury 7-day growth

Risk levels: LOW, MEDIUM, HIGH, CRITICAL

## 4. Treasury Protection
Automatic treasury mode selection:
- **ACCUMULATE** — strong bullish trend, high confidence, low risk
- **PROTECT** — moderate conditions, preserve capital
- **REDUCE_RISK** — high risk detected, reduce exposure
- **SAFE_MODE** — critical danger, no new positions

## 5. LLM AI Reasoning Engine
- Activated when: confidence < threshold, drawdown > 3%, loss_streak >= 3, extreme volatility, or critically low liquidity
- OpenAI-compatible API (`LLM_URL`, `LLM_MODEL`)
- Falls back to quant-only advisory on LLM failure
- System prompt enforces: no price prediction, no gambling, treasury-first philosophy

## 6. Telegram Commands (14 total)
| Command | Description |
|---------|-------------|
| `/help` or `/start` | Full command list |
| `/btc_status` | Account balance, equity, margin, NTL |
| `/btc_market` | Live orderbook metrics & regime |
| `/btc_advisory` | Full quant + LLM analysis on-demand |
| `/btc_treasury` | Treasury state (BTC balance, 7d growth) |
| `/btc_positions` | Open Hyperliquid positions |
| `/btc_scan` | Scanner stats, counters, last recommendation |
| `/btc_history` | Last 10 decision records from log |
| `/btc_lessons` | Last 5 self-learning lessons |
| `/btc_skills` | This document |
| `/btc_config` | Current configuration |
| `/btc_setconfig <k> <v>` | Update config key |
| `/btc_enable` / `/btc_disable` | Toggle LLM |
| `/btc_cancel` | Cancel all Hyperliquid orders |

## 7. Periodic Reporter
- Auto-reports every 5 minutes to configured Telegram chats (`TELEGRAM_REPORT_CHAT_IDS`)
- Shows: scan count, decision breakdown (approve/monitor/protect/reject), recent decisions, new lessons
- Reports only when activity exists (no empty messages)

## 8. Self-Learning System
- Every non-APPROVE recommendation becomes a timestamped lesson
- Lessons stored in `btc-lessons.json`
- Visible via `/btc_lessons` and periodic reports
- Feeds future LLM context for improved decision-making

## 9. Hyperliquid Wallet Integration
- AES-256-GCM encrypted wallet (compatible with executor-ts wallet format)
- EIP-712 signing for order placement
- Graceful degradation: runs advisory-only without wallet

---

**Source:** `btc-treasury/src/scanner.rs`, `engine.rs`, `telegram_bot.rs`, `reporter.rs`
