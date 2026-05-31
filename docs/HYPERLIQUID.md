# HyperLiquid Integration Guide

Complete guide untuk setup dan penggunaan HyperLiquid dengan BTC Treasury system.

---

## Overview

BTC Treasury system menggunakan HyperLiquid sebagai exchange untuk:
- **Spot BTC accumulation** — Buy BTC dengan USDT
- **Perpetual tracking** — Monitor BTC-PERP untuk market regime analysis
- **Treasury advisory** — AI-powered recommendations untuk treasury management

---

## Setup Steps

### 1. HyperLiquid Account

1. Buka [HyperLiquid](https://app.hyperliquid.xyz)
2. Register / Login
3. Completing KYC jika required

### 2. API Key Generation

1. Navigate ke **Account → API Keys**
2. Click **Create API Key**
3. Select permissions:
   - ✅ `read` — Read positions, balances, order status
   - ✅ `trade` — Place and cancel orders
   - ❌ `transfer` — Only if moving funds (optional)

4. **Save the API Key and Secret** — Secret only shown once!

### 3. Fund Your Account

```bash
# Deposit USDT to HyperLiquid spot wallet
# Deposit BTC untuk trading (if spot trading enabled)

# Verify balance via API
curl -X POST https://api.hyperliquid.xyz/info \
  -H "Content-Type: application/json" \
  -d '{"type":"spotBalances","user":"YOUR_ADDRESS"}'
```

### 4. Encrypt API Key

The system uses encrypted key file (`hyperliquid.enc`). Generate dengan cara:

```bash
# Method 1: Using the generate-wallet script (adapt for HyperLiquid)
cd executor-ts
npm run generate-wallet
# When prompted, enter your HyperLiquid private key instead

# Method 2: Manual encryption (see below)
```

**Manual encryption format** — `hyperliquid.enc` contains JSON:
```json
{
  "apiKey": "YOUR_API_KEY",
  "apiSecret": "YOUR_API_SECRET",
  "address": "0x...",
  "encrypted": true,
  "timestamp": "2024-01-01T00:00:00Z"
}
```

### 5. Environment Configuration

```bash
# .env file

# HyperLiquid Configuration
HYPERLIQUID_RPC_URL=https://rpc.hyperliquid.xyz/evm
HYPERLIQUID_KEY_PATH=../hyperliquid.enc  # Relative to btc-treasury
HYPERLIQUID_CHAIN_ID=133

# Exchange Configuration (HyperLiquid)
EXCHANGE_API_KEY=YOUR_HYPERLIQUID_API_KEY
EXCHANGE_API_SECRET=YOUR_HYPERLIQUID_API_SECRET
EXCHANGE_NAME=hyperliquid
EXCHANGE_BASE_URL=https://api.hyperliquid.xyz
```

---

## HyperLiquid API Reference

### Key Endpoints Used

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/info` | POST | General info, user data |
| `/exchange` | POST | Place orders, trades |

### Common API Calls

#### Get Account Balance
```bash
curl -X POST https://api.hyperliquid.xyz/info \
  -H "Content-Type: application/json" \
  -d '{"type":"balances","user":"0xYOUR_ADDRESS"}'
```

#### Get Positions
```bash
curl -X POST https://api.hyperliquid.xyz/info \
  -H "Content-Type: application/json" \
  -d '{"type":"positions","user":"0xYOUR_ADDRESS"}'
```

#### Get Market Data
```bash
curl -X POST https://api.hyperliquid.xyz/info \
  -H "Content-Type: application/json" \
  -d '{"type":"ticker","coin":"BTC"}'
```

#### Place Order (requires signature)
```bash
# See btc-treasury/src/binance.rs for implementation
# Uses HMAC-SHA256 signing for request authentication
```

---

## BTC Treasury Advisory System

### How It Works

```
Market Data (BTC-PERP) → AdvisoryEngine → Advisory Recommendation
                              ↓
                    BTC Treasury State
                              ↓
                    Telegram Notification
```

### Advisory Flow

1. **Scanner** fetches BTC market data dari HyperLiquid setiap `BTC_SCANNER_INTERVAL_SECS` (default: 120s)
2. **AdvisoryEngine** processes data melalui quant + LLM pipeline
3. **Recommendation** returned: REJECT, MONITOR, APPROVE, REDUCE_EXPOSURE, EXIT_POSITION, PROTECT_TREASURY, ENABLE_SAFE_MODE
4. **Telegram Bot** sends advisory ke whitelisted users

### Market Regimes

| Regime | Description | Recommended Action |
|--------|-------------|-------------------|
| `TRENDING_BULLISH` | Strong uptrend with volume | Consider accumulating |
| `TRENDING_BEARISH` | Strong downtrend | Protect treasury |
| `RANGING` | Sideways market | Monitor only |
| `ACCUMULATION` | Price stable, low volume | May accumulate slowly |
| `DISTRIBUTION` | Selling pressure | Reduce exposure |
| `BREAKOUT_EXPANSION` | Breakout with expansion | High risk, be careful |
| `FAKE_BREAKOUT` | Failed breakout | Avoid, protect |
| `HIGH_VOLATILITY_DANGER` | Extreme volatility | Safe mode |
| `LOW_LIQUIDITY_DANGER` | Low liquidity | Avoid trading |

### Risk Levels

| Level | Score | Action |
|-------|-------|--------|
| `LOW` | 0-2 | Normal operations |
| `MEDIUM` | 2-4 | Monitor closely |
| `HIGH` | 4-7 | Reduce exposure |
| `CRITICAL` | 7+ | Enable safe mode |

---

## Configuration

### BTC Scanner Config

```bash
# .env

# Scanner interval (seconds)
BTC_SCANNER_INTERVAL_SECS=120

# Report interval (minutes)
BTC_REPORT_INTERVAL_MINS=180

# Pairs to monitor (comma-separated)
BTC_SCANNER_PAIRS=BTC-PERP
```

### BTC Treasury Config (`data/btc-treasury/config.json`)

```json
{
  "enabled": false,
  "llmActivationThreshold": 0.75,
  "minConfidence": 0.80,
  "maxExposure": 0.50,
  "dailyLossLimitBtc": 0.0005,
  "maxConsecutiveLosses": 3,
  "safeModeVolatility": 9.0,
  "safeModeDrawdown": 0.05,
  "scannerPairs": ["BTC-PERP"]
}
```

---

## Telegram Bot Commands

| Command | Description |
|---------|-------------|
| `/status` | Treasury status, BTC holdings, USDT balance |
| `/advisory` | Get current market advisory |
| `/positions` | Open perpetual positions |
| `/config` | Show current BTC config |
| `/setconfig <key> <value>` | Update BTC config |
| `/report` | Force generate treasury report |

---

## Safety Features

### Safe Mode Triggers

- Volatility score > 9.0
- Daily drawdown > 5%
- 3+ consecutive losses
- Liquidity score < 4.0
- Spread score < 4.0

### Treasury Protection Rules

1. **Never bet entire treasury** — max exposure configurable
2. **No martingale** — no averaging down
3. **No revenge trading** — loss streak detection
4. **No predictions** — advisory only, not trading commands

---

## Troubleshooting

### API Key Issues

```bash
# Test API key validity
curl -X POST https://api.hyperliquid.xyz/info \
  -H "Content-Type: application/json" \
  -d '{"type":"meta"}'

# Common errors:
# - "Invalid signature" → Check API secret
# - "Unauthorized" → Check API key permissions
# - "Rate limited" → Reduce request frequency
```

### Key File Issues

```bash
# Verify hyperliquid.enc exists and has content
ls -la hyperliquid.enc

# File must be readable by btc-treasury container
# Check docker-compose.yml volume mount

# Test decryption by starting service and checking logs
docker compose logs btc-treasury | grep -i "decrypt\|key\|wallet"
```

### RPC Connection Issues

```bash
# Test RPC endpoint
curl -X POST https://rpc.hyperliquid.xyz/evm \
  -H "Content-Type: application/json" \
  -d '{"method":"eth_chainId","params":[],"id":1}'

# Expected response: {"jsonrpc":"2.0","id":1,"result":"0x85"}
```

### Scanner Not Running

```bash
# Check scanner status
docker compose logs btc-treasury | grep -i scanner

# Verify pairs configured
docker compose exec btc-treasury curl http://localhost:8090/btc/config

# Check interval settings
```

---

## Production Checklist

- [ ] HyperLiquid account created
- [ ] API key generated with correct permissions
- [ ] API key encrypted in `hyperliquid.enc`
- [ ] `.env` configured with HyperLiquid settings
- [ ] `.env` configured with correct paths
- [ ] USDT/BTC deposited in HyperLiquid
- [ ] BTC Treasury bot responding to `/status`
- [ ] Advisory reports being generated
- [ ] Treasury mode showing correct state
- [ ] Log monitoring active