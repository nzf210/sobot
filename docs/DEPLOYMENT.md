# Deployment Guide — Solana Hybrid System

Production deployment guide untuk VPS dengan fokus HyperLiquid integration.

---

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Project Structure](#project-structure)
3. [Environment Variables](#environment-variables)
4. [Wallet Setup](#wallet-setup)
5. [HyperLiquid Setup](#hyperliquid-setup)
6. [Build& Deploy](#build--deploy)
7. [Telegram Bot Commands](#telegram-bot-commands)
8. [Production Safety](#production-safety)
9. [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Server Requirements
- **OS**: Ubuntu 22.04 LTS (recommended)
- **CPU**: 2 cores minimum
- **RAM**: 4 GB minimum
- **Storage**: 20 GB minimum
- **Docker**: v24.0+
- **Docker Compose**: v2.20+

### Required Accounts claude-sonnet-4-5& AUTH
# ============================================
BACKEND_PORT=8089
EXECUTOR_PORT=3009
# Generate: openssl rand -hex 32
EXECUTOR_API_KEY=GENERATED_SECURE_KEY

# ============================================
# BTC TREASURY & HYPERLIQUID
# ============================================
TELEGRAM_BOT_BTC_TOKEN=YOUR_BTC_BOT_TOKEN
TELEGRAM_WHITELIST_BTC_USER_IDS=YOUR_CHAT_ID
HYPERLIQUID_RPC_URL=https://rpc.hyperliquid.xyz/evm
HYPERLIQUID_KEY_PATH=../hyperliquid.enc
HYPERLIQUID_CHAIN_ID=133
DATA_BTC_DIR=./data/btc-treasury
BTC_SCANNER_INTERVAL_SECS=120
BTC_REPORT_INTERVAL_MINS=180
BTC_SCANNER_PAIRS=BTC-PERP

# ============================================
# EXCHANGE (HyperLiquid)
# ============================================
EXCHANGE_API_KEY=YOUR_HYPERLIQUID_API_KEY
EXCHANGE_API_SECRET=YOUR_HYPERLIQUID_API_SECRET
EXCHANGE_NAME=hyperliquid
EXCHANGE_BASE_URL=https://api.hyperliquid.xyz
```

### Generate Secure Keys

```bash
# Generate API key for executor
openssl rand -hex 32

# Generate strong wallet password
openssl rand -base64 32
```

---

## Wallet Setup

### 1. Generate Encrypted Solana Wallet

```bash
cd executor-ts

# Generate new wallet
npm run generate-wallet
# → Enter private key (base58) or press Enter for new wallet
# → Enter encryption password (MUST match WALLET_PASSWORD in .env)

# Verify wallet exists
ls -la wallet.enc
```

### 2. Encrypted HyperLiquid Key

```bash
# Location: project root (../hyperliquid.enc relative to btc-treasury)
# Generate using the same process or create manually
# File path referenced in docker-compose.yml: /app/hyperliquid.enc
```

---

## HyperLiquid Setup

### Getting API Keys

1. Login ke [HyperLiquid](https://app.hyperliquid.xyz)
2. Go to **Account → API Keys**
3. Create new API key dengan permissions:
   - **Read**: Account balance, positions, orders
   - **Trade**: Place/cancel orders (if auto-trading enabled)

### Key Permissions

| Permission | Use Case |
|------------|----------|
| `read` | View positions, balances, order status |
| `trade` | Execute orders (ONLY if autoTrade enabled) |
| `transfer` | Move funds between spots/perps |

### HyperLiquid Chain ID

```bash
# Mainnet: 133
# Testnet: 133 (same, use testnet API URL)
HYPERLIQUID_RPC_URL=https://rpc.hyperliquid.xyz/evm
```

---

## Build & Deploy

### Option 1: Docker Compose (Recommended)

```bash
# 1. Clone/pull latest code
git pull origin main

# 2. Build all services
docker compose build

# 3. Start all services
docker compose up -d

# 4. Check logs
docker compose logs -f

# 5. Verify services are running
curl http://localhost:8089/health
curl http://localhost:3009/health
curl http://localhost:8090/health
```

### Option 2: Manual Build

#### Backend Go
```bash
cd backend-go
go mod tidy
go build -o solana-hybrid-backend ./cmd/main.go
./solana-hybrid-backend
```

#### Executor TypeScript
```bash
cd executor-ts
npm install
npm run build
npm start
```

#### BTC Treasury Rust
```bash
cd btc-treasury
cargo build --release
./target/release/btc-treasury
```

### Option 3: Systemd Service (Production VPS)

```bash
# /etc/systemd/system/solana-hybrid.service
[Unit]
Description=Solana Hybrid System
Requires=docker-compose.target
After=network.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/solana-hybrid-system
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
```

```bash
# Enable service
sudo systemctl enable solana-hybrid
sudo systemctl start solana-hybrid
sudo systemctl status solana-hybrid
```

---

## Telegram Bot Commands

### Solana Bot (Backend)
| Command | Description |
|---------|-------------|
| `/help` | Show all commands |
| `/analyze <token>` | Analyze a token address |
| `/status` | System health status |
| `/positions` | List open positions |
| `/close <index>` | Close specific position |
| `/closeall` | Close all positions |
| `/dryrun on\|off` | Toggle dry-run mode |
| `/config` | Show current config |
| `/setconfig <key> <value>` | Update config |

### BTC Treasury Bot
| Command | Description |
|---------|-------------|
| `/status` | Treasury status |
| `/advisory` | Get current advisory |
| `/positions` | Open positions |
| `/config` | Show config |
| `/setconfig <key> <value>` | Update config |

---

## Production Safety

### Default Safe Settings

```json
// data/memory/user-config.json
{
  "dryRun": true,        // NO real trades
  "autoTrade": false,     // Manual approval required
  "minConfidence": 0.85  // High confidence threshold
}
```

### Enable Live Trading

```bash
# Via Telegram bot - enable real trades
/dryrun off

# Via direct config update
/setconfig autoTrade true
/setconfig dryRun false
```

### Safety Checklist

- [ ] Change all default API keys
- [ ] Set strong `WALLET_PASSWORD`
- [ ] Set strong `EXECUTOR_API_KEY`
- [ ] Verify `dryRun: true` initially
- [ ] Verify `autoTrade: false` initially
- [ ] Set whitelisted Telegram IDs
- [ ] Test with dry-run before enabling live
- [ ] Monitor logs after deployment

---

## Configuration Parameters

### User Config (`data/memory/user-config.json`)

| Parameter | Default | Description |
|-----------|---------|-------------|
| `dryRun` | `true` | Dry-run mode (no real trades) |
| `autoTrade` | `false` | Auto-execute approved trades |
| `minConfidence` | `0.85` | Minimum confidence to trade |
| `minLiquiditySOL` | `100` | Minimum liquidity in SOL |
| `maxPositions` | `3` | Maximum open positions |
| `takeProfitPct` | `20` | Take profit percentage |
| `stopLossPct` | `-10` | Stop loss percentage |
| `trailingTakeProfit` | `true` | Enable trailing TP |
| `dailyLossLimitUsd` | `2` | Daily loss limit (USD) |
| `maxConsecutiveLosses` | `3` | Max consecutive losses before pause |
| `scannerIntervalSec` | `300` | Scanner check interval |

### BTC Treasury Config

| Parameter | Default | Description |
|-----------|---------|-------------|
| `enabled` | `false` | Enable BTC advisory |
| `llmActivationThreshold` | `0.75` | LLM activation confidence |
| `minConfidence` | `0.80` | Minimum advisory confidence |
| `maxExposure` | `0.50` | Maximum portfolio exposure |
| `dailyLossLimitBtc` | `0.0005` | Daily loss limit in BTC |
| `safeModeVolatility` | `9.0` | Volatility threshold for safe mode |

---

## Troubleshooting

### Service Won't Start

```bash
# Check logs
docker compose logs backend
docker compose logs executor
docker compose logs btc-treasury

# Common issues:
# 1. Port already in use → change ports in .env
# 2. Missing wallet.enc → generate wallet first
# 3. Invalid env vars → verify .env syntax
```

### Wallet Decryption Fails

```bash
# Verify WALLET_PASSWORD matches
# Check wallet.enc exists and is readable
ls -la executor-ts/wallet.enc

# Regenerate if needed
cd executor-ts
npm run generate-wallet
```

### HyperLiquid Connection Issues

```bash
# Test RPC connectivity
curl -X POST https://rpc.hyperliquid.xyz/evm \
  -H "Content-Type: application/json" \
  -d '{"method":"eth_chainId","params":[],"id":1}'

# Check API key validity
# Verify HYPERLIQUID_KEY_PATH points to valid encrypted file
```

### Telegram Bot Not Responding

```bash
# Verify bot token
curl https://api.telegram.org/bot<TOKEN>/getMe

# Check whitelist
# TELEGRAM_WHITELIST_USER_IDS must include your chat ID
```

### Build Failures

```bash
# Go backend
cd backend-go && go mod tidy && go build ./cmd/main.go

# TypeScript executor
cd executor-ts && npm install && npm run build

# Rust treasury
cd btc-treasury && cargo build --release
```

---

## VPS Deployment Checklist

1. [ ] Server provisioned (Ubuntu 22.04)
2. [ ] Docker installed
3. [ ] Project cloned
4. [ ] `.env` configured with real keys
5. [ ] Solana wallet encrypted (`wallet.enc`)
6. [ ] HyperLiquid key encrypted (`hyperliquid.enc`)
7. [ ] Telegram bot tokens configured
8. [ ] LLM API key configured
9. [ ] Build verified (`docker compose build`)
10. [ ] Services started (`docker compose up -d`)
11. [ ] Health checks passed
12. [ ] Telegram bot responding
13. [ ] Dry-run mode verified
14. [ ] Log monitoring active

---

## Data Persistence

All data is stored in JSON files:

```
data/memory/
├── user-config.json      # Trading configuration
├── pool-memory.json      # Positions & PnL
├── decision-log.json     # Pipeline decisions
├── lessons.json          # Learned lessons
├── strategies.json       # Trading strategies
└── signal-weights.json   # Signal configuration

data/btc-treasury/
├── treasury-state.json   # BTC holdings
├── advisory-log.json     # Advisory history
└── config.json          # BTC config
```

**Backup regularly** — these files contain all trading history and learned patterns.
