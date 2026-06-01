# BTC Treasury Bot — Production Readiness Audit
**Date**: 2026-06-01  
**Auditor**: Claude Code (thorough)  
**Status**: 🔴 **NOT PRODUCTION-READY** — Critical architectural gaps

---

## Executive Summary

The BTC Treasury bot has **well-structured code** and a **thoughtful design document** (SKILL.md), but the actual **runtime code path is severely disconnected** from the documented architecture. The bot in its current state is a **market scanner with LLM advisory**, NOT an autonomous BTC accumulation bot. The gap between documentation and implementation is not cosmetic — the entire accumulation pipeline (indicator computation, AI scoring engine, risk-managed execution) is **compiled but orphaned** — never called by the runtime.

**Overall Readiness**: ~35%

---

## Critical Issues (BLOCKERS)

### 🔴 CRITICAL-1: ExecutionEngine is declared but NEVER instantiated

**File**: `src/main.rs`, `src/execution_engine.rs`

The `ExecutionEngine` has full buy/sell/treasury-split logic (194 lines), but it is **never constructed or used** anywhere in the runtime. Grep confirms zero references to `ExecutionEngine` outside its own file. The scanner runs every 15 minutes but only calls `engine.analyze()` and logs the result — it **never executes a trade**.

```rust
// src/scanner.rs — scan_pair() ends with:
let advisory = engine.analyze(&input).await;
// ... update stats ...
// ... log decision ...
// ... generate lesson ...
// ❌ NO EXECUTION CALL — trade is never placed
```

**Impact**: The bot will NEVER autonomously accumulate BTC. All trading must be manual via Telegram.

**Fix**: Integrate `ExecutionEngine` into the scanner loop. After APPROVE advisory, check risk manager, then call `execution_engine.execute_buy()`.

---

### 🔴 CRITICAL-2: Entire engines/ module is orphaned (never called at runtime)

**Files**: `src/engines/ai_scoring.rs`, `rs_engine.rs`, `momentum_engine.rs`, `volume_engine.rs`, `risk_manager.rs`

These 5 engine files contain 502 lines of production-quality scoring logic (RS Engine, Momentum Engine, Volume Engine, AI Scoring, Risk Manager). They are **compiled** (via `mod engines` in main.rs) but **never called** from any runtime code path. The only references are internal cross-calls within `ai_scoring.rs` and unit tests.

The actual scanner uses `engine.rs::AdvisoryEngine::opportunity_score()` — a completely separate, much simpler scoring function that uses only orderbook-derived fields (liquidity_score, spread_score, volatility_score, etc.) with hardcoded weights.

**Impact**: The sophisticated quant pipeline described in SKILL.md (RS 40%, Volume 25%, Trend 20%, Vol 10%, Structure 5%) **does not exist at runtime**. All decisions are based on a crude orderbook heuristic.

**Fix**: Wire the scanner to fetch klines, compute PairMetrics via indicators.rs, then call AIScoringEngine::score_pair() and RiskManager::assess() before the advisory engine.

---

### 🔴 CRITICAL-3: PairMetrics and OHLCV pipeline is completely disconnected

**Files**: `src/binance.rs`, `src/indicators.rs`, `src/models.rs`

- `BinanceClient.get_klines()` exists and works — fetches OHLCV candles from Binance
- `indicators.rs` computes EMA, RSI, MACD, VWAP, ATR from `Vec<Ohlcv>`
- `PairMetrics` struct has all fields: RS scores, EMA values, MACD, RSI, ATR, volume flags
- **BUT**: The scanner never calls `get_klines()`, never computes indicators, never populates `PairMetrics`

The scanner only calls `exchange.get_market_data()` which returns `BtcMarketData` — a struct with only orderbook-derived scores. The `PairMetrics` struct with 40+ computed fields sits unused except in unit tests.

**Impact**: No technical indicators are computed at runtime. The bot trades blind to EMA, MACD, RSI, ATR, volume profile, and relative strength.

**Fix**: Add a step in `scan_pair()` that calls `binance.get_klines()` for 15m/1h/4h/1d timeframes, runs indicators, populates `PairMetrics`, and feeds it to the AI scoring engines.

---

### 🔴 CRITICAL-4: BTC price hardcoded at $65,000 in two places

**Files**: `src/execution_engine.rs:180`, `src/memory.rs:164`

```rust
// execution_engine.rs
let btc_price = 65_000.0; // TODO: fetch real BTCUSDT price

// memory.rs  
let btc_price = 65_000.0; // will be overridden by actual price if available
```

The TODOs acknowledge the problem. Every treasury BTC accounting calculation uses this hardcoded price. When BTC is at $100k, profit calculations will be off by ~54%. When BTC is at $30k, they'll be off by ~117%.

**Impact**: BTC treasury accounting (the core metric) is completely unreliable.

**Fix**: Fetch real-time BTCUSDT price from Binance before computing treasury updates.

---

### 🔴 CRITICAL-5: Config env var names don't match .env.sample

**Files**: `src/config.rs` vs `.env.sample`

| config.rs reads | .env.sample provides |
|---|---|
| `EXCHANGE_API_KEY` | `BINANCE_API_KEY` |
| `EXCHANGE_API_SECRET` | `BINANCE_API_SECRET` |
| `TELEGRAM_BOT_BTC_TOKEN` | _(missing)_ |
| `TELEGRAM_WHITELIST_USER_BTC_IDS` | _(missing)_ |

Additionally, `TELEGRAM_BOT_BTC_TOKEN` and `TELEGRAM_WHITELIST_USER_BTC_IDS` are BTC-treasury specific env vars that are NOT in `.env.sample`.

**Impact**: Users who follow `.env.sample` will have their Binance keys ignored. The Telegram bot will never start because the token variable name doesn't match.

**Fix**: Either rename config.rs to read `BINANCE_API_KEY`/`BINANCE_API_SECRET`, or update `.env.sample` to use `EXCHANGE_API_KEY`/`EXCHANGE_API_SECRET`. Add missing Telegram env vars.

---

### 🔴 CRITICAL-6: Auto trading pause never triggers automatically

**Files**: `src/scanner.rs`, `src/engines/risk_manager.rs`

The `RiskManager::should_pause()` detects when loss streak >= 3. The `BtcTreasuryState::trading_paused_until` field exists and the scanner checks it. The Telegram bot has `/btc_pause` and `/btc_resume` commands.

**BUT**: There is NO code that automatically sets `trading_paused_until` when 3 consecutive losses occur. The scanner reads `trading_paused_until` and skips if paused, but nothing writes to it except the manual Telegram commands.

**Impact**: The 3-loss-streak auto-pause rule (a core risk control) is non-functional. Loss streaks can continue indefinitely without automatic intervention.

**Fix**: In the position monitor's close handler, after a losing close, check loss streak and auto-set `trading_paused_until` for 24 hours.

---

## High Severity Issues

### 🟠 HIGH-1: Quant fallback TP/SL values are dangerous

**File**: `src/engine.rs:428-431`

```rust
dynamic_take_profit: 20.0,   // quant fallback default
dynamic_stop_loss: -10.0,    // quant fallback default
```

When the LLM is disabled or fails, the quant fallback uses TP=20% and SL=-10%. The documented parameters are TP=3-8% and SL=1-2%. These fallback values violate the risk framework by 2-5x and would be triggered in the most common path (LLM disabled by default, `cfg.enabled = false`).

**Impact**: With default config (`enabled: false`), every advisory uses 20% TP and -10% SL. A position using these parameters on $50 capital risks $5 (10x the documented 1% max risk).

**Fix**: Change defaults to `dynamic_take_profit: 5.5` and `dynamic_stop_loss: -1.5` to match config defaults.

---

### 🟠 HIGH-2: MemoryStore uses global mutex for all file I/O

**File**: `src/memory.rs:9-10`

```rust
pub struct MemoryStore {
    lock: RwLock<()>,  // global mutex
}
```

All operations — reads, writes, position saves, treasury updates, lesson logging — contend on one `RwLock`. The position monitor polls every 30s, scanner runs every 15min per pair, Telegram commands can come at any time, and the reporter runs periodically. All serialized.

**Impact**: Under concurrent load (multiple Telegram commands during a scanner cycle), operations will queue up. Not catastrophic for low-frequency use but unnecessary contention.

**Fix**: Use per-file locks or an async-friendly approach (tokio::sync::RwLock per file, or switch to SQLite).

---

### 🟠 HIGH-3: Dry run mode has no effect on scanner

**File**: `src/scanner.rs:184-186`

```rust
if config.dry_run {
    tracing::debug!("Scanner [{}]: dry_run mode active", pair);
}
```

The scanner logs that dry run is active, but it never executes trades anyway (Critical-1). If auto-execution is added, this log-only approach means dry_run won't actually prevent trades.

**Impact**: When auto-execution is wired up, dry_run=true will still execute real trades unless explicitly gated.

**Fix**: Add a guard before any execution call: `if !config.dry_run { execution_engine.execute_buy(...).await; }`

---

### 🟠 HIGH-4: PositionMonitor's pnl_btc field stores percentage, not BTC

**File**: `src/position_monitor.rs:82`

```rust
positions[i].pnl_btc = pnl_pct;  // stores e.g. 5.5 for 5.5% gain
```

The field is named `pnl_btc` (implying BTC-denominated PnL) but stores percentage. This is used downstream in the loss streak calculation:

```rust
// scanner.rs
if pos.pnl_btc < 0.0 { streak += 1; }
```

This happens to work because both wins and losses are stored as percentages (positive/negative), but it's semantically wrong and will cause bugs if anyone tries to display `pnl_btc` as actual BTC value.

**Impact**: Data model corruption — field meaning doesn't match field name. Loss streak calculation works by coincidence.

**Fix**: Rename field to `pnl_pct` and add a separate `pnl_btc` computed from `size * entry_price * pnl_pct / 100`.

---

### 🟠 HIGH-5: No reconnection/retry logic for exchange API failures

**File**: `src/scanner.rs`, `src/binance.rs`

When Binance API calls fail, the scanner logs the error and moves to the next pair. There's no:
- Exponential backoff
- Retry with jitter
- Circuit breaker
- Alert on repeated failures

**Impact**: During Binance API degradation, all pairs will fail silently each scan cycle without recovery attempts. No alert is sent to the operator.

**Fix**: Add retry with exponential backoff (3 attempts, 1s/2s/4s delay) in the Binance client. Add error rate tracking to the reporter.

---

## Medium Severity Issues

### 🟡 MED-1: No graceful shutdown handling

**File**: `src/main.rs:173-175`

```rust
loop {
    tokio::time::sleep(std::time::Duration::from_secs(3600)).await;
}
```

The main function enters an infinite sleep loop with no signal handler for SIGTERM/SIGINT. Docker will force-kill after the stop timeout. Any in-flight scanner operations or position monitor checks will be abruptly terminated.

**Impact**: Potential for corrupted JSON files if killed mid-write. Docker stop will take the full timeout period.

**Fix**: Use `tokio::signal::ctrl_c()` to catch shutdown signals, then gracefully await in-flight operations.

---

### 🟡 MED-2: MemoryStore JSON files have no atomic writes

**File**: `src/memory.rs`

```rust
fn write_json<T: serde::Serialize>(&self, filename: &str, data: &T) {
    let json = serde_json::to_string_pretty(data).expect("Failed to serialize");
    fs::write(&path, json).expect("Failed to write file");
}
```

Direct `fs::write` truncates the file before writing. If the process crashes mid-write, the file is corrupted with partial JSON.

**Impact**: Data loss on crash — corrupt btc-positions.json or btc-treasury.json requires manual recovery.

**Fix**: Write to a temp file, then `fs::rename` (atomic on Linux) to replace the target.

---

### 🟡 MED-3: Loss streak calculation depends on in-memory positions only

**File**: `src/scanner.rs:206-214`

```rust
let loss_streak = {
    let mut streak = 0;
    for pos in stored_positions.iter().rev() {
        if pos.pnl_btc < 0.0 { streak += 1; } else { break; }
    }
    streak
};
```

Loss streak is computed from currently open positions' PnL. But positions are removed from the store when closed. The streak should be computed from the decision log (which persists closed trades) or a dedicated loss counter in treasury state.

**Impact**: If 3 losing trades happen over several hours and all positions were closed (removed from store), the loss streak will be 0 when the next scan runs. The 3-loss auto-pause will never trigger from closed positions.

**Fix**: Track consecutive losses in `BtcTreasuryState` with a dedicated counter that persists across closes.

---

### 🟡 MED-4: No circuit breaker for exchange errors

If Binance returns consistent errors (API key revoked, IP banned, rate limited), the scanner will continue to attempt all pairs every 15 minutes, logging errors each time. There's no mechanism to detect systemic failure and pause scanning.

**Impact**: Flood of error logs, wasted CPU cycles, no operator notification.

**Fix**: Track consecutive exchange errors. After N consecutive failures across all pairs, pause scanning and notify via Telegram.

---

### 🟡 MED-5: Telegram bot whitelist is shared/confusing env var names

The BTC treasury bot uses `TELEGRAM_WHITELIST_USER_BTC_IDS` while the Solana bot uses `TELEGRAM_WHITELIST_USER_IDS`. If a user sets only the latter (which is in .env.sample), the BTC treasury bot sees an empty whitelist, which means "allow all" (due to `is_whitelisted()` returning true for empty whitelist).

**Impact**: Security bypass — if whitelist env var is misspelled or missing, ALL Telegram users can control the bot.

**Fix**: Change `is_whitelisted()` to reject all when whitelist is empty, or clearly separate the env var names. Add the BTC-specific vars to .env.sample.

---

### 🟡 MED-6: LLM prompt has no token limit check

The system prompt includes SKILL.md (214 lines, ~2K chars), lessons context (variable), and positions JSON. With gpt-4o-mini (128K context window) this is unlikely to overflow, but with smaller models or accumulated lessons, there's no truncation.

**Impact**: LLM calls could fail silently with context length errors on smaller models or after months of accumulated lessons.

**Fix**: Estimate token count before sending, truncate lessons context if approaching model limit.

---

## Minor Issues

### 🔵 MINOR-1: Duplicate treasury split logic

`execution_engine.rs::apply_treasury_split()` and `memory.rs::update_treasury_on_close()` both implement 50/50 split logic with different accounting. If both were ever called for the same close, the treasury would double-count.

---

### 🔵 MINOR-2: EMA function may produce NaN with zero prices

`indicators.rs::ema()` divides by `period as f64` for initial SMA. If all candle closes are 0.0, the EMA sequence will be all zeros but the logic handles this. However, edge cases with very small prices in BTC-quote pairs (like `SHIBBTC` at 0.00000001) could cause floating-point precision issues in ATR and RSI calculations.

---

### 🔵 MINOR-3: Reporter sends to all chat IDs without per-chat error isolation

If one chat ID is invalid, the loop continues but logs an error. This is fine, but repeated delivery failures to an invalid chat ID waste API calls each cycle.

---

### 🔵 MINOR-4: Scanner interval applies to ALL pairs sequentially

With 10 pairs and 500ms delay between them, a full scan cycle takes ~5 seconds of the 15-minute interval. This is fine for now but doesn't scale well if many pairs are added.

---

### 🔵 MINOR-5: `PairMetrics::default()` sets `rsi_14` to 50.0

Setting default RSI to neutral 50.0 (rather than 0.0) means uncomputed metrics won't be obviously broken — they'll look valid but be wrong. All other fields default to 0.0 or false.

---

### 🔵 MINOR-6: Hyperliquid `get_current_price` uses raw reqwest, not signed_post

The Hyperliquid adapter's price fetch bypasses the normal auth pathway (goes to public `/info` endpoint with raw POST). This is intentional (it's a public endpoint) but inconsistent with the rest of the client's signed approach. Fails if Hyperliquid changes their CORS/auth policy.

---

## Architecture Gap Analysis

### Documented Pipeline (SKILL.md) vs Actual Runtime

| Step | Documented | Actually Running | Status |
|------|-----------|-----------------|--------|
| 1. Market Scanner (15 min) | ✅ Fetches OHLCV 15m/1h/4h/1d | ⚠️ Fetches only orderbook, no OHLCV | PARTIAL |
| 2. BTC Pair Universe | ✅ Auto-discover from Binance | ⚠️ Manual add only, `/btc_discover` just shows list | PARTIAL |
| 3. Relative Strength Engine | ✅ RS = Coin Return - BTC Return | ❌ Not called at runtime | MISSING |
| 4. Momentum Engine | ✅ EMA/MACD/RSI/ATR | ❌ Not called at runtime | MISSING |
| 5. Volume Engine | ✅ Spike/Expansion/Wash Trade | ❌ Not called at runtime | MISSING |
| 6. AI Scoring Model | ✅ 40/25/20/10/5 weighted | ❌ Not called; uses crude orderbook heuristic | MISSING |
| 7. Risk Manager | ✅ 1% risk, max 1 pos, 3-loss pause | ⚠️ Logic exists but not wired; auto-pause missing | PARTIAL |
| 8. Execution Engine | ✅ Market Buy → TP/SL → Sell | ❌ Never instantiated or called | MISSING |
| 9. Position Monitor | ✅ 30s polling, TP/SL/trailing | ✅ Runs correctly | WORKING |
| 10. BTC Treasury Manager | ✅ 50/50 compound/vault split | ⚠️ Logic exists but hardcoded $65k BTC price | BUGGY |
| 11. Self-Learning | ✅ Lessons from non-APPROVE | ✅ Works for scanner decisions | WORKING |

**Summary**: 5 of 11 pipeline steps are MISSING at runtime. 3 are PARTIAL. Only 3 work correctly.

---

## Data Flow Reality

```
ACTUAL RUNTIME:
  Scanner (15min) → exchange.get_market_data()  [only orderbook]
                  → AdvisoryEngine.opportunity_score()  [crude heuristic]
                  → AdvisoryEngine.analyze()  [quant + optional LLM]
                  → log decision → DONE
                  
  PositionMonitor (30s) → check TP/SL/trailing → close if hit
                        → update treasury (hardcoded $65k)

  Telegram Bot → manual commands only
              → /btc_buy, /btc_sell, /btc_close work (manual execution)

DOCUMENTED BUT NOT RUNNING:
  get_klines() → Indicators (EMA/RSI/MACD/ATR) → PairMetrics
  → RSEngine → MomentumEngine → VolumeEngine
  → AIScoringEngine(40/25/20/10/5) → RiskManager
  → ExecutionEngine → auto buy/sell
```

---

## Production Readiness Score

| Category | Score | Status |
|----------|-------|--------|
| Execution Engine | 0% | ❌ Not wired — bot can't auto-trade |
| Strategy Logic (Quant) | 0% | ❌ Orphaned engines, no OHLCV pipeline |
| Risk Management | 30% | ⚠️ Logic exists, auto-pause broken, fallback TP/SL dangerous |
| Position Monitoring | 85% | ✅ Works, but pnl_btc semantic bug |
| State Management | 60% | ⚠️ No atomic writes, hardcoded BTC price, global mutex |
| Error Handling | 20% | ❌ No retry, no backoff, no circuit breaker |
| Exchange Integration | 75% | ✅ Binance client solid, env var name mismatch |
| LLM Integration | 70% | ⚠️ No token limit check, dangerous quant fallback |
| Telegram Bot | 90% | ✅ 25+ commands functional |
| Documentation/Code Sync | 25% | ❌ SKILL.md describes pipeline that doesn't run |
| Configuration | 50% | ⚠️ Env var mismatch, missing vars from .env.sample |
| **Overall** | **~35%** | 🔴 **NOT READY** |

---

## Fix Priority & Estimated Effort

### Phase 1: Make the bot trade (2-3 days)
1. **Wire ExecutionEngine** — Instantiate in main, call from scanner on APPROVE
2. **Connect OHLCV pipeline** — Scanner calls get_klines → indicators → PairMetrics
3. **Wire AI engines** — Call AIScoringEngine + RiskManager in scan_pair
4. **Fix env var names** — Align config.rs with .env.sample
5. **Fix fallback TP/SL** — Change 20%/-10% to 5.5%/-1.5%

### Phase 2: Make risk controls work (1-2 days)
6. **Auto-pause on 3-loss streak** — Monitor triggers pause automatically
7. **Fix BTC price** — Fetch real-time BTCUSDT before treasury accounting
8. **Fix loss streak calculation** — Use decision log, not open positions
9. **Dry run guard** — Add dry_run check before execution calls
10. **Fix pnl_btc semantics** — Store percentage separately from BTC value

### Phase 3: Production hardening (1-2 days)
11. **Atomic file writes** — Write to temp + rename
12. **API retry logic** — Exponential backoff in Binance client
13. **Graceful shutdown** — tokio::signal handler
14. **Circuit breaker** — Pause scanning on repeated exchange errors
15. **Token limit check** — Truncate LLM context if needed
16. **Fix whitelist empty=allow** — Reject all when whitelist empty

### Phase 4: Polish (1 day)
17. Deduplicate treasury split logic
18. Add missing env vars to .env.sample
19. Per-chat error isolation in reporter
20. Comprehensive integration test

---

## Verification Checklist Before Production

- [ ] Scanner fetches OHLCV for all timeframes per pair
- [ ] Indicators compute EMA/RSI/MACD/ATR from live data
- [ ] RS Engine computes relative strength vs BTC
- [ ] AI Scoring produces scores from all 5 weighted components
- [ ] Risk Manager gates execution (max 1 pos, 1% risk, loss streak pause)
- [ ] ExecutionEngine places real market buys on APPROVE
- [ ] PositionMonitor closes at dynamic TP/SL with real BTCUSDT price
- [ ] Treasury split correctly accounts BTC using live BTC price
- [ ] 3-loss streak auto-pauses for 24 hours
- [ ] Dry run mode prevents all real execution
- [ ] Env vars match .env.sample
- [ ] Telegram whitelist rejects when empty
- [ ] Graceful shutdown saves state cleanly
- [ ] Atomic file writes prevent corruption
- [ ] API failures retry with backoff
- [ ] Circuit breaker pauses on systemic exchange failure

---

## Files Requiring Changes

### Must Change (Phase 1-2)
- `src/main.rs` — Wire ExecutionEngine, fetch BTCUSDT price at startup
- `src/scanner.rs` — Add OHLCV fetch, indicator compute, engine scoring, execution call
- `src/engine.rs` — Fix fallback TP/SL values
- `src/config.rs` — Align env var names with .env.sample
- `src/memory.rs` — Atomic writes, fix BTC price
- `src/position_monitor.rs` — Auto-pause trigger, fix pnl_btc semantics

### Should Change (Phase 3)
- `src/binance.rs` — Add retry with backoff
- `src/telegram_bot.rs` — Tighten whitelist empty behavior
- `.env.sample` — Add BTC treasury env vars

### Consider Changing (Phase 4)
- `src/execution_engine.rs` — Deduplicate treasury split
- `src/reporter.rs` — Error isolation per chat
- `src/indicators.rs` — NaN guards for tiny prices

---

## Conclusion

The BTC Treasury bot has solid foundations: clean Rust code, well-designed engine modules, proper exchange integration, and a comprehensive Telegram interface. However, the **runtime code path is critically incomplete**. The gap between documented architecture and actual execution means the bot cannot autonomously accumulate BTC.

The core pipeline — OHLCV → Indicators → AI Scoring → Risk Management → Execution — exists in code but is **not wired together**. The bot currently operates as a market scanner with an LLM advisor. All trading must be manual.

**Recommendation**: Do not deploy to production until Phase 1 and Phase 2 fixes are complete. The estimated effort is 3-5 days for a developer familiar with the codebase. After fixes, a full integration test with Binance testnet is essential before going live with real capital.
