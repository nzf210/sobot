# BTC Treasury Accumulation AI — Skills

> **Mission:** Akumulasi BTC secara konsisten melalui Binance Spot trading.
> Target: BTC(t+1) > BTC(t). Setiap trade harus menambah BTC holdings.
> Modal: $50. Maks 1 posisi aktif. Maks 1% risiko per trade.
> TP: 3-8%. SL: 1-2%. AI Score ≥ 80 = AMBIL POSISI, < 80 = DO NOTHING.

## Exchange

**Binance Spot ONLY.** No futures, no perpetual, no leverage.

## Pair Format

BTC-Quote pairs: `SYMBOLBTC`
Examples: `SOLBTC`, `ETHBTC`, `SUIBTC`, `LINKBTC`, `DOGEBTC`, `ADABTC`

Price reference: `BTCUSDT` (BTC price in USDT for RS calculations)

## Architecture Pipeline

```
Market Scanner (every 15 min)
        ↓
BTC Pair Universe (auto-discover BTC-quote pairs)
        ↓
Relative Strength Engine (RS = Coin Return - BTC Return)
        ↓
Momentum Engine (EMA, MACD, RSI, Volume Growth, ATR)
        ↓
Volume Engine (Spike, Expansion, Wash Trade filter)
        ↓
AI Scoring Model (40% RS, 25% Vol, 20% Trend, 10% VolQual, 5% Structure)
        ↓
Risk Manager (1% risk, max 1 pos, 3-loss pause, 10% drawdown reduce)
        ↓
Execution Engine (Market Buy → TP/SL monitor → Market Sell)
        ↓
BTC Treasury Manager (50/50 compound/treasury vault split)
```

## 1. Market Scanner

- Poll interval: **every 15 min** (`BTC_SCANNER_INTERVAL_SECS` env var)
- Fetch OHLCV: **15m, 1h, 4h, 1d candles** per pair
- Fetch BTCUSDT return per timeframe for RS calculation
- Dynamic pair universe: auto-discover BTC-quote pairs from Binance
- Default pair: `BTCUSDT` (for price reference)

## 2. Relative Strength Engine

- RS Score = Coin Return - BTC Return
- Weight: **1h 35%, 4h 30%, 1d 25%, 15m 10%**
- RS Rising = **1h RS > 4h RS** = accelerating momentum
- BTCUSDT is used as the BTC return benchmark

## 3. Momentum Engine

- **EMA Alignment**: EMA20 > EMA50 > EMA200 = bullish
- **MACD Bullish**: MACD line > Signal line AND histogram > 0
- **RSI(14)**: ideal range 40-60 for continuation
- **Volume Growth**: current volume vs average volume
- **ATR Expansion**: volatility expanding

## 4. Volume Engine

- **Volume Spike**: current vol > 2x average = spike detected
- **Volume Expansion**: 1h + 4h volume both above their averages
- **Wash Trade Filter**: reject if wide spread + low movement + high volume
- **Liquidity Check**: reject thin pairs

## 5. AI Scoring Model

| Component          | Weight | Metric                          |
|--------------------|--------|---------------------------------|
| Relative Strength  | 40%    | RS score (0-10)                 |
| Volume Growth      | 25%    | Volume spike + expansion        |
| Trend Strength     | 20%    | EMA alignment + MACD bullish    |
| Volatility Quality | 10%    | ATR% 1-5% ideal                 |
| Market Structure   | 5%     | Spread + RS rising              |

**Score ≥ 80 → AMBIL POSISI. Score < 80 → DO NOTHING. Cash is a position.**

## 6. Risk Manager

| Parameter              | Value          |
|------------------------|----------------|
| Max risk per trade     | **1%** modal   |
| Max positions          | **1**          |
| Max loss per trade     | 1% × $50 = $0.50 |
| 3 loss streak          | **Pause 24 hours** |
| Drawdown > 10%         | **Reduce size 50%** |
| Position size formula  | risk_amount / SL_distance |

## 7. Entry Conditions — ALL must be met

- RS Rising (1h RS > 4h RS)
- EMA20 > EMA50 > EMA200 (bullish alignment)
- MACD Bullish
- Volume > Average
- **AI Score ≥ 80**

## 8. Exit Conditions

- **Take Profit**: 3-8% (dynamic based on regime via LLM)
- **Trailing Stop**: active — track highest price, trigger on X% drop from peak
- **Stop Loss**: 1-2% (hard limit)
- TP > |SL| — always maintain positive expected value

## 9. BTC Accounting

Always measured in BTC, not USD:

```json
{
  "btc_before": "0.00100000",
  "btc_after": "0.00102500",
  "btc_gain": "0.00002500"
}
```

## 10. Treasury Management

- **Profit Split** on every winning close:
  - **50% → Compound Balance** (re-enter capital)
  - **50% → BTC Treasury Vault** (never traded)
- BTC Treasury Vault grows over time
- After position close → market sell → PnL → split → update `current_btc`

## 11. Anti-FOMO Rules

- ❌ Martingale
- ❌ Averaging Down
- ❌ Revenge Trading
- ❌ YOLO Trade
- ❌ All-In
- 3 consecutive losses → Pause 24 Hours
- Drawdown > 10% → Reduce Position 50%

## 12. Telegram Commands

| Command                    | Description                              |
|----------------------------|------------------------------------------|
| `/btc_status`              | Binance Spot balance + BTC holdings      |
| `/btc_market [PAIR]`       | Live market data + OHLCV                 |
| `/btc_advisory [PAIR]`     | Full quant + LLM advisory                |
| `/btc_treasury`            | BTC holdings, vault, compound, stats      |
| `/btc_positions`           | Open positions with TP/SL/trailing       |
| `/btc_pairs`               | List active BTC-quote pairs              |
| `/btc_addpair <PAIR>`      | Add pair (e.g. SOLBTC, ETHBTC, SUIBTC)   |
| `/btc_removepair <PAIR>`   | Remove pair from scanner                 |
| `/btc_discover`            | Show popular BTC-quote pairs to add      |
| `/btc_pairinfo <PAIR>`     | Detailed AI scores for one pair         |
| `/btc_scan [PAIR]`         | Scanner stats + AI scores                |
| `/btc_history`             | Last 10 decisions                        |
| `/btc_lessons`             | Recent self-learning lessons             |
| `/btc_config`              | Current config (TP/SL/thresholds)        |
| `/btc_setconfig <k> <v>`   | Update config live                       |
| `/btc_enable`              | Enable LLM advisory                      |
| `/btc_disable`             | Disable LLM advisory                     |
| `/btc_buy <SIZE> <PAIR>`   | Market buy + dynamic TP/SL               |
| `/btc_sell`                | Close position at market price            |
| `/btc_cancel`              | Cancel all open orders                   |
| `/btc_skills`              | Full capabilities                         |
| `/help`                    | Help message                             |

**PAIR format**: `SYMBOLBTC` (e.g. `SOLBTC`, `ETHBTC`, `DOGEBTC`, `SUIBTC`)

## 13. Trading Flow

```
SCAN (15 min) → Fetch OHLCV → Compute RS/EMA/MACD/RSI/ATR
     ↓
AI SCORE all pairs → Rank → Select if Score ≥ 80 AND max 1 pos
     ↓
RISK CHECK → 1% risk, max 1 pos, loss streak < 3
     ↓
ADVISORY (LLM) → Set dynamic TP/SL
     ↓
EXECUTE BUY → Binance Market Buy → Record position
     ↓
MONITOR (30s) → Check TP/SL/Trailing
     ↓
TP/SL HIT → Binance Market Sell → BTC accounting → 50/50 split
     ↓
BTC Treasury grows → Ready for next position
```

## 14. Self-Learning

- Every non-APPROVE decision → lesson logged to `btc-lessons.json`
- Lessons cover: RS validity, volume signal quality, momentum alignment
- Feed into future LLM context for improved decisions

## 15. Configuration

| Config Key            | Default  | Description                      |
|-----------------------|----------|----------------------------------|
| `take_profit_pct`     | 5.5%     | Default take profit              |
| `stop_loss_pct`       | -1.5%    | Default stop loss (negative)    |
| `trailing_tp_pct`     | 3.0%     | Trailing stop percentage         |
| `use_trailing`        | true     | Enable trailing stop             |
| `min_score_threshold` | 80.0     | AI score threshold               |
| `risk_per_trade_pct`  | 1.0%     | Risk per trade                  |
| `max_positions`       | 1        | Max concurrent positions         |
| `compound_pct`        | 50%      | Compound on winning close        |
| `treasury_pct`        | 50%      | BTC vault split on winning close |
| `initial_capital_usdt`| $50      | Initial capital                  |
| `max_consecutive_losses` | 3     | Pause after this many losses     |

---
**Sources:** `src/engines/`, `src/indicators.rs`, `src/execution_engine.rs`, `src/engine.rs`, `src/models.rs`
**Exchange:** Binance Spot ONLY
**Goal:** Continuously grow BTC holdings through disciplined spot trading.
