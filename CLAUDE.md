# Solana Hybrid System — Claude Code Context

## Overview

Production-oriented hybrid trading system yang mengautomasi penemuan token Solana, analisis, dan eksekusi. Sistem memiliki tiga layanan independen yang berkomunikasi via HTTP dan internal Docker network.

**Kemampuan inti**: Automated Solana meme/DeFi token sniping via DLMM dan Jupiter, multi-engine analysis pipeline (momentum, regime, risk, LLM reasoning), BTC treasury advisory service, dan Telegram bot untuk kontrol operasional penuh.

---

## Architecture

```
docker-compose.yml (solana-net bridge)

backend-go (Go/Gin, port 8080)
  ├── orchestrator (14-stage pipeline)
  ├── scanner (PumpFun/Raydium/Meteora watchers)
  ├── executor client (HTTP → executor-ts)
  ├── position manager (TP/SL/trailing)
  ├── telegram bot (/analyze, /config, /status, dll)
  └── 14 engines (momentum, regime, risk, confidence, dll)

executor-ts (Node/Express, port 3000)
  ├── Jupiter Swap v6 executor
  ├── Encrypted wallet loader
  └── DLMM deployment stub

btc-treasury (Rust/Actix, port 8090)
  ├── AdvisoryEngine (hybrid quant + LLM)
  └── BTC Telegram bot
```

---

## Services

### backend-go (port 8080)

**Stack**: Go, Gin, SQLite (via go-sqlite3), Zap logger

**Key packages**:
- `internal/api/server.go` — Gin HTTP server; endpoints: `GET /health`, `POST /analyze`
- `internal/orchestrator/pipeline.go` — 14-stage pipeline orchestrator (the brain)
- `internal/engines/*.go` — 14 specialized engines (see Pipeline below)
- `internal/llm/reasoning.go` — OpenAI-compatible LLM caller; fallback heuristics when disabled
- `internal/scanner/scanner.go` — Token scanner; 4-worker goroutine pool, deduplication dengan TTL
- `internal/scanner/watchers.go` — PumpFunWatcher, RaydiumWatcher, MeteoraWatcher
- `internal/scanner/reporter.go` — Periodic Telegram reports + rejection lessons
- `internal/executor/client.go` — Direct HTTP swap + wallet balance calls ke executor-ts
- `internal/executor/pipeline_executor.go` — `ExecuteBuy`, `ExecuteSell`, `DeployDLMM`, `GetWalletBalance`
- `internal/manager/manager.go` — Position manager; polls every 10s, checks TP/SL/trailing, closes positions
- `internal/manager/monitor.go` — Periodic Telegram status reports
- `internal/manager/momentum.go` — Momentum analyzer; computes trailing stop % based on momentum level
- `internal/risk/engine.go` — Pre-pipeline token filter (liquidity, volume, mcap, quality, pair age, price sanity)
- `internal/scoring/scoring.go` — Fast confidence scorer (0-1); score >= 0.6 = high-quality for AI
- `internal/memory/memory.go` — JSON file-based store; persists positions, lessons, strategies, user config
- `internal/notifier/telegram.go` — Telegram broadcast to multiple chat IDs
- `internal/telegram/bot.go` — Full Telegram bot: `/analyze`, `/config`, `/setconfig`, `/status`, `/positions`, `/close`, `/closeall`, `/dryrun`, `/help`
- `internal/models/token.go` — `TokenMetrics` struct (DexScreener fields, organic score, wash trade)
- `internal/models/position.go` — `Position` struct (entry/exit price/amount, PnL USD, timestamps)

**API endpoints**:
- `GET /health` — Health check
- `POST /analyze` — Run full pipeline on a token address; triggers Telegram notification; executes trade if approved

---

### executor-ts (port 3000)

**Stack**: TypeScript, Node.js, Express, Solana Web3.js, Jupiter SDK, Meteora SDK (stub)

**Key files**:
- `src/index.ts` — Express server; auth middleware (X-API-Key); routes: `/health`, `/wallet`, `/execute`, `/deploy-dlmm`
- `src/executors/jupiter.ts` — Jupiter Swap v6: get quote → get transaction → sign with wallet → send on-chain
- `src/wallets/wallet.ts` — `loadWallet()`: reads `wallet.enc`, decrypts with `WALLET_PASSWORD` env var, returns `Keypair`
- `src/wallets/crypto.ts` — `encrypt()`/`decrypt()` using AES-256-GCM with scrypt KDF + random salt/IV
- `src/scripts/generate-wallet.ts` — CLI tool; supports Solana (Ed25519, base58) and Hyperliquid (secp256k1, hex)

**API endpoints**:
- `GET /health` — Health check
- `GET /wallet` — Returns wallet public key + SOL balance
- `POST /execute` — Executes Jupiter swap (SOL → token or token → SOL); requires `X-API-Key` header
- `POST /deploy-dlmm` — Placeholder DLMM deployment

**Execution flow**: Wallet (encrypted `.enc`) → Jupiter Quote API → Swap transaction → Sign with keypair → Submit to Solana RPC

---

### btc-treasury (port 8090)

**Stack**: Rust, Actix-web

**Key files**:
- `src/main.rs` — Entry point; initializes config, engine, optional Binance client, scanner, reporter, Telegram bot
- `src/server.rs` — Actix-web server; 8 REST endpoints
- `src/engine.rs` — `AdvisoryEngine` — hybrid quant + LLM BTC trading advisor; 10-regime classifier; risk scoring; treasury mode logic
- `src/models.rs` — All data types: `BtcMarketData`, `BtcTreasuryState`, `BtcAdvisoryInput`, `BtcAdvisoryPosition`, `BtcConfig`, `FullBtcAdvisory`
- `src/llm.rs` — OpenAI-compatible LLM client
- `src/scanner.rs` — Binance pair scanner (async, configurable interval)
- `src/reporter.rs` — Periodic Telegram treasury reports
- `src/telegram_bot.rs` — BTC Telegram bot with commands
- `src/binance.rs` — Binance Spot API client with HMAC-SHA256 signing

**API endpoints**:
- `GET /health`
- `POST /btc/advisory` — Analyze market data → advisory recommendation
- `GET /btc/treasury` / `POST /btc/treasury`
- `POST /btc/market` — Submit market update; returns advisory with loss streak analysis
- `GET /btc/positions`
- `GET /btc/config` / `POST /btc/config`

**AdvisoryEngine**: Pure advisory (no auto-execution). Risk levels: LOW/MEDIUM/HIGH/CRITICAL. Treasury modes: ACCUMULATE/PROTECT/REDUCE_RISK/SAFE_MODE. Recommendations: REJECT/MONITOR/APPROVE/REDUCE_EXPOSURE/EXIT_POSITION/PROTECT_TREASURY/ENABLE_SAFE_MODE.

---

## 14-Stage Pipeline (`PipelineOrchestrator.Process()`)

Sequential stages:

1. **Metrics Normalization** — clamp negatives, normalize ratios to 0-10, scores to 0-1
2. **Metric-only Rule Check** — fast rejection on liquidity, volume, organic score, wash trade, market cap
3. **Deployer Reputation Engine** — fetches deployer history from DexScreener; scores rugs vs total tokens (0-1)
4. **Wallet Cluster Detection** — detects coordinated bot/shill activity from BSR, volume, wash trade heuristics
5. **Holder Distribution Engine** — queries Solscan for top-10 holder %; falls back to market cap/liquidity proxy
6. **Liquidity Stability Engine** — maintains sliding window of 10 snapshots; detects "rug" (>30% drop), "shrinking", "growing"
7. **Jupiter Intelligence** — queries Jupiter quote API for price impact at our order size; caches 2 min
8. **Momentum Engine** — volume acceleration ratio, price momentum Z-score, combined 0-1 score + direction
9. **Market Regime Detector** — classifies SOL trend as bull/sideways/bear; caches 5 min
10. **Confidence Engine** — weighted combination of 7 factors (organic 20%, momentum 20%, deployer 15%, holders 15%, liquidity 10%, Jupiter 10%, regime 10%)
11. **Engine-dependent Rule Check** — confidence gate, deployer reputation, holder concentration, liquidity stability, wallet cluster, Jupiter impact, momentum, regime, position size
12. **Dynamic Position Sizing** — maps confidence to size: >=0.85=full, >=0.70=75%, >=0.60=50%, >=0.50=25%; bear market = 50% penalty
13. **Portfolio Risk Engine** — validates max open positions, capital at risk, bear market with existing positions, daily loss limit, consecutive losses
14. **LLM Narrative Analysis** — sends full pipeline context + historical memory to LLM; gets BUY/SELL/HOLD/MICRO_ENTRY_ONLY decision with confidence

**Final**: if LLM decision is BUY or MICRO_ENTRY_ONLY and confidence >= threshold and size > 0 → trade approved and executed (live or dry-run).

### Engine Details

| Engine | File | What it does |
|--------|------|-------------|
| `MomentumEngine` | `engines/engines.go` | Volume acceleration (vol5m/expected from vol1h), price Z-score, momentum score/direction |
| `MarketRegimeDetector` | `engines/engines.go` | Fetches SOL pair from DexScreener; bull/sideways/bear from 5m+1h price change |
| `ConfidenceEngine` | `engines/engines.go` | Weighted factor combination into final 0-1 score |
| `DynamicSizer` | `engines/engines.go` | Confidence-to-size mapping with bear market penalty |
| `PortfolioRiskEngine` | `engines/engines.go` | Max positions, capital at risk, daily loss, consecutive losses |
| `DeployerReputationEngine` | `engines/intelligence.go` | DexScreener search by deployer address; rug rate heuristic |
| `HolderDistributionEngine` | `engines/intelligence.go` | Solscan top-10 holders API; fallback to mcap/liquidity proxy |
| `LiquidityStabilityEngine` | `engines/intelligence.go` | Sliding window of 10 snapshots; rug/shrinking/stable/growing |
| `WalletClusterDetector` | `engines/intelligence.go` | BSR + tx count + wash trade probability heuristics |
| `JupiterIntelligence` | `engines/intelligence.go` | Jupiter quote API for slippage; 2-min cache |
| `LLMNarrativeAnalysis` | `engines/narrative.go` | Full pipeline context + bot memory → LLM → structured JSON decision |
| `RuleEngine` | `engines/rule_engine.go` | Three validation layers: `ValidateMetrics`, `ValidateEngines`, `ValidatePortfolio` |

---

## Configuration

### `configs/default.json`
```json
{
  "min_liquidity_usd": 10000,
  "max_positions": 5,
  "sniper_size_sol": 0.1,
  "llm_enabled": true,
  "daily_loss_limit": 2
}
```

### Environment Variables (`.env`)
| Variable | Description |
|----------|-------------|
| `RPC_URL` | Solana RPC endpoint |
| `WSS_URL` | Solana WebSocket endpoint |
| `WALLET_PASSWORD` | Password to decrypt `wallet.enc` |
| `WALLET_PATH` | Encrypted wallet file path (default: `executor-ts/wallet.enc`) |
| `DATA_DIR` | JSON store directory (default: `./data/memory`) |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token |
| `TELEGRAM_WHITELIST_USER_IDS` | Comma-separated Telegram chat IDs |
| `LLM_API_KEY` | OpenAI API key |
| `LLM_MODEL` | LLM model (default: `gpt-4o-mini`) |
| `LLM_URL` | LLM API base URL |
| `LLM_ENABLED` | Enable LLM analysis (default: `true`) |
| `BACKEND_PORT` | Go backend port (default: `8080`) |
| `EXECUTOR_PORT` | TS executor port (default: `3000`) |
| `EXECUTOR_API_KEY` | Shared API key between backend and executor |
| `LOG_LEVEL` | Zap log level (debug/info/warn/error) |

### User Config (runtime, `data/memory/user-config.json`)
Controls trading behavior at runtime: `autoTrade`, `dryRun`, `scannerIntervalSec`, `minConfidence`, `minLiquiditySOL`, `maxLiquiditySOL`, `minVolumeSOL`, `minOrganicScore`, `maxWashTradePct`, `minMcapSOL`, `maxMcapSOL`, `maxTop10Pct`, `maxDeployAmountSol`, `takeProfitPct`, `stopLossPct`, `trailingTakeProfit`, `maxOpenPositions`, `dailyLossLimitUsd`, `maxConsecutiveLosses`.

---

## Data Storage (JSON, `data/memory/`)

| File | Content |
|------|---------|
| `user-config.json` | Runtime trading config |
| `pool-memory.json` | Open and closed positions with PnL |
| `decision-log.json` | Full history of pipeline decisions |
| `lessons.json` | Self-taught lessons from trade outcomes |
| `strategies.json` | Named trading strategies |
| `signal-weights.json` | Weighted factors for signal scoring |
| `SKILL.md` | Bot skills description |

---

## Key Behavior Notes

### Momentum Sniper
Not a separate sniper component — the "sniper" behavior emerges from the pipeline:
- **Scanner** watches for newly created pools (30-60 min age limits per DEX)
- **Metrics Fetcher** waits 10 seconds then retries 3x to let DexScreener index new pairs
- **Rule Engine** applies liquidity/volume/quality gates
- **Dynamic Sizer** scales position based on confidence
- **Executor** calls Jupiter Swap v6 to buy the token
- **Position manager** monitors with smart trailing TP (adjusts trail % based on momentum: Low=10%, Medium=12%, High=15%, Extreme=20%)

### DLMM Automation
The `DeployDLMM` endpoint in executor-ts is a **stub** — returns hardcoded success. `LLMDLMMSuitability` score is computed by LLM narrative engine but actual Meteora DLMM SDK integration is not yet implemented.

### LLM Reasoning Pipeline
1. **Fast path (heuristic fallback)**: if `LLM_ENABLED=false` or no API key, returns `MICRO_ENTRY_ONLY` with fixed scores
2. **Full LLM path**: `PipelineOrchestrator` builds a prompt with bot memory context (strategies, lessons, signal weights, user config) + all metrics + all engine outputs; asks for: decision (BUY/SELL/HOLD/MICRO_ENTRY_ONLY), confidence, narrative_score, dlmm_suitability, reasoning

### BTC AdvisoryEngine
Separate LLM pipeline with system prompt enforcing strict BTC treasury philosophy (protect capital, measure in BTC, no predictions, no martingale).

---

## Security

- AES-256-GCM encrypted wallet files (`scrypt` KDF from password)
- Path traversal prevention on wallet file paths
- X-API-Key auth between backend and executor
- Telegram whitelist by chat ID
- DRY RUN mode as default (no real trades until explicitly disabled)
- `.gitignore` covers all `.env`, `*.enc`, wallet JSON, `.db` files

---

## Running

```bash
# Go backend
cd backend-go && go mod tidy && go run ./cmd/main.go

# TS executor
cd executor-ts && npm install && npm run dev

# Docker (all services)
docker-compose up -d

# Generate encrypted wallet
cd executor-ts && npm run generate-wallet
```

---

## Telegram Bot Commands

`/help`, `/analyze <token>`, `/health`, `/status`, `/positions`, `/close <index>`, `/closeall`, `/dryrun on|off`, `/config`, `/setconfig <key> <value>` (supports bool and float values at runtime)