# Solana Hybrid System

Production-ready hybrid trading system untuk Solana token sniper dengan BTC treasury advisory.

## Documentation

| Document | Description |
|----------|-------------|
| [DEPLOYMENT.md](docs/DEPLOYMENT.md) | VPS deployment guide |
| [HYPERLIQUID.md](docs/HYPERLIQUID.md) | HyperLiquid integration guide |
| [PARAMETERS.md](docs/PARAMETERS.md) | All configuration parameters |
| [docs/architecture.md](docs/architecture.md) | System architecture |

## Quick Start

```bash
# 1. Configure environment
cp .env.sample .env
# Edit .env with your API keys

# 2. Generate encrypted wallet
cd executor-ts
npm run generate-wallet
cd ..

# 3. Start services
docker compose up -d

# 4. Verify
curl http://localhost:8089/health
```

## Services

| Service | Port | Language | Purpose |
|---------|------|----------|---------|
| backend-go | 8089 | Go | Pipeline orchestrator, Telegram bot |
| executor-ts | 3009 | TypeScript | Jupiter swap execution |
| btc-treasury | 8090 | Rust | BTC treasury advisory |

## Safety

**Default settings are SAFE for production:**
- `dryRun: true` — No real trades
- `autoTrade: false` — Manual approval required
- Strong API keys enforced

## Quick Links

- [Deployment Guide](docs/DEPLOYMENT.md)
- [HyperLiquid Setup](docs/HYPERLIQUID.md)
- [Configuration Reference](docs/PARAMETERS.md)