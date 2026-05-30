package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/engines"
	"hybrid-solana-bot/internal/executor"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/models"
	"hybrid-solana-bot/internal/notifier"
)

// PipelineOrchestrator runs the full analysis pipeline from metrics to execution.
type PipelineOrchestrator struct {
	cfg                  config.Config
	mem                  *memory.MemoryStore
	log                  *zap.Logger
	ruleEngine           *engines.RuleEngine
	momentumEngine       *engines.MomentumEngine
	regimeDetector       *engines.MarketRegimeDetector
	confidenceEngine     *engines.ConfidenceEngine
	deployerEngine       *engines.DeployerReputationEngine
	holderEngine         *engines.HolderDistributionEngine
	liquidityEngine      *engines.LiquidityStabilityEngine
	walletCluster        *engines.WalletClusterDetector
	jupiterIntel         *engines.JupiterIntelligence
	llmAnalysis          *engines.LLMNarrativeAnalysis
	dynamicSizer         *engines.DynamicSizer
	portfolioRisk        *engines.PortfolioRiskEngine
	pipelineExecutor     *executor.PipelineExecutor
	notifier             *notifier.TelegramNotifier
	userConfig           memory.UserConfig
}

// NewPipeline creates a fully wired pipeline orchestrator.
func NewPipeline(cfg config.Config, mem *memory.MemoryStore, log *zap.Logger) *PipelineOrchestrator {
	userCfg := mem.GetUserConfig()

	// Create rule engine config from user settings
	ruleCfg := engines.RuleEngineConfig{
		MinConfidenceScore:  userCfg.MinConfidence,
		MinLiquiditySOL:     userCfg.MinLiquiditySOL,
		MaxLiquiditySOL:     userCfg.MaxLiquiditySOL,
		MinVolume5mSOL:      userCfg.MinVolumeSOL,
		MinOrganicScore:     userCfg.MinOrganicScore,
		MaxWashTradePct:     userCfg.MaxWashTradePct,
		MinMarketCapSOL:     userCfg.MinMcapSOL,
		MaxMarketCapSOL:     userCfg.MaxMcapSOL,
		MaxTop10HolderPct:   userCfg.MaxTop10Pct,
		MinHolderCount:      50,
		MaxDeployAmountSOL:  userCfg.MaxDeployAmountSol,
		AllowBearMarket:     false,
		MaxOpenPositions:    userCfg.MaxOpenPositions,
		MaxCapitalAtRiskSOL: float64(cfg.MaxPositions) * cfg.SniperSizeSOL * 1.5,
		DryRun:              userCfg.DryRun,
		DailyLossLimitUsd:   userCfg.DailyLossLimitUsd,
		MaxConsecutiveLosses: userCfg.MaxConsecutiveLosses,
	}

	return &PipelineOrchestrator{
		cfg:                  cfg,
		mem:                  mem,
		log:                  log,
		userConfig:           userCfg,
		ruleEngine:           engines.NewRuleEngine(ruleCfg),
		momentumEngine:       engines.NewMomentumEngine(),
		regimeDetector:       engines.NewMarketRegimeDetector(),
		confidenceEngine:     engines.NewConfidenceEngine(),
		deployerEngine:       engines.NewDeployerReputationEngine(),
		holderEngine:         engines.NewHolderDistributionEngine(),
		liquidityEngine:      engines.NewLiquidityStabilityEngine(),
		walletCluster:        engines.NewWalletClusterDetector(),
		jupiterIntel:         engines.NewJupiterIntelligence(),
		llmAnalysis:          engines.NewLLMNarrativeAnalysis(cfg.LLMURL, cfg.LLMModel, cfg.LLMAPIKey, mem, log),
		dynamicSizer:         engines.NewDynamicSizer(0.01, cfg.SniperSizeSOL),
		portfolioRisk:        engines.NewPortfolioRiskEngine(),
		pipelineExecutor:     executor.NewPipelineExecutor(cfg.ExecutorHost, cfg.ExecutorPort, log),
		notifier:             notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramWhitelistUserIDs),
	}
}

// Process runs the full pipeline on a token's metrics and returns the result.
func (p *PipelineOrchestrator) Process(metrics models.TokenMetrics) *engines.PipelineSignal {
	sig := &engines.PipelineSignal{
		Metrics:  metrics,
		Source:   "scanner",
		SeenAt:   time.Now(),
		Approved: false,
	}

	// ── Stage 1: Metrics Normalization ──────────────────────────────────────
	// Already handled in metrics fetcher, but we do additional normalization here
	sig = p.normalizeMetrics(sig)

	// ── Stage 2: Rule Engine (initial validation) ───────────────────────────
	isValid, violations := p.ruleEngine.Validate(sig)
	if !isValid {
		sig.RejectedBy = fmt.Sprintf("Rule Engine: %s", formatViolations(violations))
		p.log.Info("Signal rejected by Rule Engine",
			zap.String("token", sig.Metrics.Token),
			zap.String("reason", sig.RejectedBy),
		)
		return sig
	}

	// ── Stage 3: Deployer Reputation ─────────────────────────────────────────
	p.deployerEngine.Analyze(sig)

	// ── Stage 4: Wallet Cluster Detection ────────────────────────────────────
	p.walletCluster.Analyze(sig)

	// ── Stage 5: Holder Distribution ─────────────────────────────────────────
	p.holderEngine.Analyze(sig)

	// ── Stage 6: Liquidity Stability ─────────────────────────────────────────
	p.liquidityEngine.Analyze(sig)

	// ── Stage 7: Jupiter Intelligence ────────────────────────────────────────
	p.jupiterIntel.Analyze(sig, p.cfg.SniperSizeSOL)

	// ── Stage 8: Momentum Analysis ───────────────────────────────────────────
	p.momentumEngine.Analyze(sig)

	// ── Stage 9: Market Regime Detection ─────────────────────────────────────
	p.regimeDetector.Analyze(sig)

	// ── Stage 10: Confidence Scoring ─────────────────────────────────────────
	p.confidenceEngine.Compute(sig)

	// ── Stage 11: Dynamic Position Sizing ────────────────────────────────────
	p.dynamicSizer.Size(sig)

	// ── Stage 12: Portfolio Risk Engine ──────────────────────────────────────
	positions := p.mem.GetPositions()
	openCount := 0
	totalAtRisk := 0.0
	for _, pos := range positions {
		if !pos.IsClosed {
			openCount++
			totalAtRisk += pos.EntryAmount
		}
	}

	walletResp, err := p.pipelineExecutor.GetWalletBalance()
	walletBalance := 0.0
	if err == nil && walletResp != nil {
		walletBalance = walletResp.BalanceSol
	}

	// Calculate daily loss and consecutive losses
	dailyLossUsd := p.calculateDailyLossUsd(positions)
	consecutiveLosses := p.calculateConsecutiveLosses(positions)

	portValid, portViolations := p.ruleEngine.ValidatePortfolio(sig, openCount, totalAtRisk, walletBalance, dailyLossUsd, consecutiveLosses)
	if !portValid {
		sig.RejectedBy = fmt.Sprintf("Portfolio Risk: %s", formatViolations(portViolations))
		p.log.Info("Signal rejected by Portfolio Risk Engine",
			zap.String("token", sig.Metrics.Token),
			zap.String("reason", sig.RejectedBy),
		)
		return sig
	}

	// ── Stage 13: LLM Narrative Analysis ─────────────────────────────────────
	p.llmAnalysis.Analyze(sig)

	// ── Final decision: APPROVE or REJECT ────────────────────────────────────
	if sig.LLMDecision == "BUY" || sig.LLMDecision == "MICRO_ENTRY_ONLY" {
		if sig.ConfidenceScore >= p.userConfig.MinConfidence && sig.RecommendedSizeSOL > 0 {
			sig.Approved = true
			p.log.Info("Signal APPROVED",
				zap.String("token", sig.Metrics.Token),
				zap.Float64("confidence", sig.ConfidenceScore),
				zap.Float64("size_sol", sig.RecommendedSizeSOL),
				zap.String("llm_decision", sig.LLMDecision),
			)

			// Execute trade
			go p.executeTrade(sig)
		}
	}

	return sig
}

func (p *PipelineOrchestrator) normalizeMetrics(sig *engines.PipelineSignal) *engines.PipelineSignal {
	m := &sig.Metrics

	// Clamp negative values
	if m.LiquidityUSD < 0 {
		m.LiquidityUSD = 0
	}
	if m.LiquiditySOL < 0 {
		m.LiquiditySOL = 0
	}
	if m.MarketCap < 0 {
		m.MarketCap = 0
	}
	if m.MarketCapSOL < 0 {
		m.MarketCapSOL = 0
	}
	if m.Volume5m < 0 {
		m.Volume5m = 0
	}
	if m.Volume5mSOL < 0 {
		m.Volume5mSOL = 0
	}
	if m.Volume1h < 0 {
		m.Volume1h = 0
	}

	// Normalize buy/sell ratio to 0-10 range
	if m.BuySellRatio < 0 {
		m.BuySellRatio = 0
	}
	if m.BuySellRatio > 10 {
		m.BuySellRatio = 10
	}

	// Normalize scores to 0-1
	if m.OrganicScore < 0 {
		m.OrganicScore = 0
	}
	if m.OrganicScore > 100 {
		m.OrganicScore = 100
	}
	if m.WashTradeProbability < 0 {
		m.WashTradeProbability = 0
	}
	if m.WashTradeProbability > 1 {
		m.WashTradeProbability = 1
	}

	return sig
}

func (p *PipelineOrchestrator) executeTrade(sig *engines.PipelineSignal) {
	userCfg := p.userConfig

	// ── DRY RUN mode ────────────────────────────────────────────────────────
	if userCfg.DryRun {
		positions := p.mem.GetPositions()
		newPos := models.Position{
			TokenAddress: sig.Metrics.Token,
			EntryPrice:   sig.Metrics.PriceSOL,
			EntryAmount:  sig.RecommendedSizeSOL,
			AmountToken:  0,
			EntryTime:    time.Now().UTC(),
			HighestPrice: sig.Metrics.PriceSOL,
			IsClosed:     false,
		}
		positions = append(positions, newPos)
		p.mem.SavePositions(positions)

		msg := fmt.Sprintf(
			"🧪 *[DRY RUN] Pipeline BUY*\n"+
				"*Token:* `%s`\n"+
				"*Size:* %.4f SOL\n"+
				"*Confidence:* %.0f%%\n"+
				"*Momentum:* %s (%.2f)\n"+
				"*Regime:* %s\n"+
				"*LLM:* %s (%.0f%%)\n"+
				"⚠️ _No real transaction executed._",
			sig.Metrics.Token,
			sig.RecommendedSizeSOL,
			sig.ConfidenceScore*100,
			sig.MomentumDirection, sig.MomentumScore,
			sig.MarketRegime,
			sig.LLMDecision, sig.LLMConfidence*100,
		)
		if err := p.notifier.SendMessage(msg); err != nil {
			p.log.Error("Failed to send DRY RUN notification", zap.Error(err))
		}
		p.log.Info("DRY RUN trade recorded", zap.String("token", sig.Metrics.Token))
		return
	}

	// ── LIVE TRADING ────────────────────────────────────────────────────────
	txHash, err := p.pipelineExecutor.ExecuteBuy(sig)
	if err != nil {
		p.log.Error("LIVE trade execution failed",
			zap.String("token", sig.Metrics.Token),
			zap.Error(err),
		)
		msg := fmt.Sprintf(
			"❌ *LIVE Trade Failed*\n*Token:* `%s`\n*Error:* %s",
			sig.Metrics.Token, err.Error(),
		)
		p.notifier.SendMessage(msg)
		return
	}

	// Record position
	positions := p.mem.GetPositions()
	newPos := models.Position{
		TokenAddress: sig.Metrics.Token,
		EntryPrice:   sig.Metrics.PriceSOL,
		EntryAmount:  sig.RecommendedSizeSOL,
		AmountToken:  0,
		EntryTime:    time.Now().UTC(),
		HighestPrice: sig.Metrics.PriceSOL,
		IsClosed:     false,
	}
	positions = append(positions, newPos)
	p.mem.SavePositions(positions)

	// Log decision
	p.mem.LogDecision(sig.Metrics.Token, sig.Metrics, sig.LLMDecision,
		fmt.Sprintf("Confidence: %.2f, Size: %.4f SOL, TxHash: %s", sig.ConfidenceScore, sig.RecommendedSizeSOL, txHash))

	// Notify
	msg := fmt.Sprintf(
		"✅ *LIVE Trade Executed!*\n"+
			"*Token:* `%s`\n"+
			"*Size:* %.4f SOL\n"+
			"*Confidence:* %.0f%%\n"+
			"*LLM:* %s\n"+
			"*TxHash:* `%s`",
		sig.Metrics.Token,
		sig.RecommendedSizeSOL,
		sig.ConfidenceScore*100,
		sig.LLMDecision,
		txHash,
	)
	if err := p.notifier.SendMessage(msg); err != nil {
		p.log.Error("Failed to send trade notification", zap.Error(err))
	}
}

func formatViolations(violations []engines.RuleViolation) string {
	if len(violations) == 0 {
		return ""
	}
	msgs := make([]string, len(violations))
	for i, v := range violations {
		msgs[i] = v.Message
	}
	return strings.Join(msgs, "; ")
}

func (p *PipelineOrchestrator) calculateDailyLossUsd(positions []models.Position) float64 {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	totalLoss := 0.0
	
	for _, pos := range positions {
		if pos.IsClosed && pos.ExitTime.After(today) && pos.ProfitLossUsd < 0 {
			totalLoss += -pos.ProfitLossUsd
		}
	}
	
	return totalLoss
}

func (p *PipelineOrchestrator) calculateConsecutiveLosses(positions []models.Position) int {
	// Sort by exit time (newest first)
	sorted := make([]models.Position, len(positions))
	copy(sorted, positions)
	
	consecutive := 0
	for i := len(sorted) - 1; i >= 0; i-- {
		pos := sorted[i]
		if !pos.IsClosed {
			continue
		}
		if pos.ProfitLossUsd < 0 {
			consecutive++
		} else {
			break
		}
	}
	
	return consecutive
}
