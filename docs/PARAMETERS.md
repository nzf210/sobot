# Configuration Parameters Reference

Complete reference untuk semua parameter yang bisa dikonfigurasi di sistem.

---

## Environment Variables (`.env`)

### Solana Network
| Variable | Default | Description |
|----------|---------|-------------|
| `RPC_URL` | — | Solana RPC endpoint (Chainstack, Helius, etc.) |
| `WSS_URL` | — | Solana WebSocket endpoint |

### Wallet
| Variable | Default | Description |
|----------|---------|-------------|
| `WALLET_PASSWORD` | — | Password untuk decrypt wallet.enc |
| `WALLET_PATH` | `executor-ts/wallet.enc` | Path ke encrypted wallet |

### Executor API
| Variable | Default | Description |
|----------|---------|-------------|
| `EXECUTOR_PORT` | `3009` | Port untuk executor-ts service |
| `EXECUTOR_API_KEY` | — | API key untuk auth antara backend & executor |

### Telegram
| Variable | Default | Description |
|----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | — | Bot token dari @BotFather |
| `TELEGRAM_WHITELIST_USER_IDS` | — | Comma-separated chat IDs yang boleh akses |

### LLM
| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_API_KEY` | — | API key untuk LLM provider |
| `LLM_MODEL` | `gpt-4o-mini` | Model yang digunakan |
| `LLM_URL` | — | API base URL |
| `LLM_ENABLED` | `true` | Enable/disable LLM analysis |
| `LLM_TEMPERATURE` | `0.2` | Sampling temperature |

### BTC Treasury & HyperLiquid
| Variable | Default | Description |
|----------|---------|-------------|
| `TELEGRAM_BOT_BTC_TOKEN` | — | BTC bot token |
| `TELEGRAM_WHITELIST_BTC_USER_IDS` | — | BTC bot whitelist |
| `HYPERLIQUID_RPC_URL` | `https://rpc.hyperliquid.xyz/evm` | HyperLiquid RPC |
| `HYPERLIQUID_KEY_PATH` | `../hyperliquid.enc` | Path ke encrypted HyperLiquid key |
| `HYPERLIQUID_CHAIN_ID` | `133` | Chain ID (133 = HyperLiquid mainnet) |
| `DATA_BTC_DIR` | `./data/btc-treasury` | BTC treasury data directory |
| `BTC_SCANNER_INTERVAL_SECS` | `120` | Market scan interval (detik) |
| `BTC_REPORT_INTERVAL_MINS` | `180` | Report interval (menit) |
| `BTC_SCANNER_PAIRS` | `BTC-PERP` | Pairs untuk di-monitor |

### Exchange
| Variable | Default | Description |
|----------|---------|-------------|
| `EXCHANGE_API_KEY` | — | Exchange API key |
| `EXCHANGE_API_SECRET` | — | Exchange API secret |
| `EXCHANGE_NAME` | `hyperliquid` | Exchange name |
| `EXCHANGE_BASE_URL` | `https://api.hyperliquid.xyz` | Exchange API base URL |

---

## User Config (`data/memory/user-config.json`)

### Trading Mode
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `dryRun` | `true` | bool | Dry-run mode (no real trades) |
| `autoTrade` | `false` | bool | Auto-execute approved trades |

### Confidence & Quality Gates
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `minConfidence` | `0.85` | 0-1 | Minimum confidence untuk trade |
| `minOrganicScore` | `70` | 0-100 | Minimum organic score |
| `maxWashTradePct` | `25` | 0-100 | Maximum wash trade percentage |

### Liquidity & Volume
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `minLiquiditySOL` | `100` | SOL | Minimum liquidity |
| `maxLiquiditySOL` | `66000` | SOL | Maximum liquidity |
| `minVolumeSOL` | `33` | SOL | Minimum 24h volume |
| `minMcapSOL` | `1000` | SOL | Minimum market cap |
| `maxMcapSOL` | `66000` | SOL | Maximum market cap |

### Position Management
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `maxOpenPositions` | `3` | int | Maximum open positions |
| `maxDeployAmountSol` | `0.03` | SOL | Max SOL per trade |
| `maxTop10Pct` | `50` | 0-100 | Max top 10 holder percentage |

### Take Profit / Stop Loss (Fallback)
> **Note:** Jika LLM mengembalikan dynamic TP/SL, nilai per-position akan override nilai global ini.

| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `takeProfitPct` | `20` | % | Default take profit percentage (fallback) |
| `stopLossPct` | `-10` | % | Default stop loss percentage (fallback) |
| `trailingTakeProfit` | `true` | bool | Enable trailing take profit |

### Risk Management
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `dailyLossLimitUsd` | `2` | USD | Daily loss limit |
| `maxConsecutiveLosses` | `3` | int | Max consecutive losses before pause |

### Scanner
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `scannerIntervalSec` | `300` | detik | Scanner check interval |

### LLM
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `llmTemperature` | `0.2` | 0-1 | LLM sampling temperature |

---

## BTC Treasury Config (`data/btc-treasury/config.json`)

### Enable/Disable
| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable BTC advisory system |

### Confidence & Threshold
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `llmActivationThreshold` | `0.75` | 0-1 | Confidence threshold untuk LLM activation |
| `minConfidence` | `0.80` | 0-1 | Minimum advisory confidence |

### Risk Limits
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `maxExposure` | `0.50` | 0-1 | Maximum portfolio exposure |
| `dailyLossLimitBtc` | `0.0005` | BTC | Daily loss limit in BTC |
| `maxConsecutiveLosses` | `3` | int | Max consecutive losses |

### Safe Mode Thresholds
| Parameter | Default | Range | Description |
|-----------|---------|-------|-------------|
| `safeModeVolatility` | `9.0` | 0-10 | Volatility threshold for safe mode |
| `safeModeDrawdown` | `0.05` | 0-1 | Drawdown threshold for safe mode |

### Scanner
| Parameter | Default | Description |
|-----------|---------|-------------|
| `scannerPairs` | `["BTC-PERP"]` | Pairs to monitor |

---

## Default Config (`configs/default.json`)

| Parameter | Default | Description |
|-----------|---------|-------------|
| `min_liquidity_usd` | `10000` | Minimum liquidity in USD |
| `max_positions` | `5` | Maximum positions |
| `sniper_size_sol` | `0.1` | Sniper position size |
| `llm_enabled` | `true` | Enable LLM |
| `daily_loss_limit` | `2` | Daily loss limit |

---

## Quick Start Config Values

### Conservative (Safe)
```json
{
  "dryRun": true,
  "autoTrade": false,
  "minConfidence": 0.90,
  "maxOpenPositions": 2,
  "maxDeployAmountSol": 0.01,
  "dailyLossLimitUsd": 1,
  "maxConsecutiveLosses": 2
}
```

### Moderate (Balanced)
```json
{
  "dryRun": false,
  "autoTrade": false,
  "minConfidence": 0.80,
  "maxOpenPositions": 3,
  "maxDeployAmountSol": 0.05,
  "dailyLossLimitUsd": 5,
  "maxConsecutiveLosses": 3
}
```

### Aggressive (High Risk)
```json
{
  "dryRun": false,
  "autoTrade": true,
  "minConfidence": 0.70,
  "maxOpenPositions": 5,
  "maxDeployAmountSol": 0.1,
  "dailyLossLimitUsd": 10,
  "maxConsecutiveLosses": 5
}
```

---

## Telegram Config Update

Use `/setconfig` command untuk update values via Telegram:

```
/setconfig dryRun false
/setconfig autoTrade true
/setconfig minConfidence 0.80
/setconfig maxOpenPositions 5
/setconfig takeProfitPct 25
/setconfig stopLossPct -15
```

---

## Config Validation

### Hard Limits (Cannot Override)
- `dryRun: true` required untuk initial deployment
- `autoTrade: false` required sampai explicitly enabled
- `dailyLossLimitUsd > 0` required

### Soft Limits (Warnings Only)
- `minConfidence < 0.5` → Warning: Very low threshold
- `maxOpenPositions > 10` → Warning: High position count
- `maxDeployAmountSol > 1` → Warning: Large position size