package scanner

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"btc-treasury/internal/engine"
	"btc-treasury/internal/engine/engines"
	"btc-treasury/internal/exchange"
	"btc-treasury/internal/indicators"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/models"
	"btc-treasury/internal/monitor"
)

type RecentDecision struct {
	Pair           string  `json:"pair"`
	Timestamp      string  `json:"timestamp"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	RiskLevel      string  `json:"risk_level"`
	Reason         string  `json:"reason"`
}

type ScannerStats struct {
	Scanned         atomic.Uint64
	AdvisoryApprove atomic.Uint64
	AdvisoryMonitor atomic.Uint64
	AdvisoryProtect atomic.Uint64
	AdvisoryReject  atomic.Uint64
	Errors          atomic.Uint64
}

type ScannerStatsSnapshot struct {
	Scanned uint64
	Approve uint64
	Monitor uint64
	Protect uint64
	Reject  uint64
	Errors  uint64
}

func (s *ScannerStats) Snapshot() ScannerStatsSnapshot {
	return ScannerStatsSnapshot{
		Scanned: s.Scanned.Load(),
		Approve: s.AdvisoryApprove.Load(),
		Monitor: s.AdvisoryMonitor.Load(),
		Protect: s.AdvisoryProtect.Load(),
		Reject:  s.AdvisoryReject.Load(),
		Errors:  s.Errors.Load(),
	}
}

type PairState struct {
	Stats              ScannerStats
	lastScanTimeLock   sync.RWMutex
	lastScanTime       string
	lastRegimeLock     sync.RWMutex
	lastRegime         string
	lastRecLock        sync.RWMutex
	lastRecommendation string
	lastConfLock       sync.RWMutex
	lastConfidence     float64
	lastRiskLock       sync.RWMutex
	lastRiskLevel      string
	lastReasonLock     sync.RWMutex
	lastReason         string
}

func NewPairState() *PairState {
	return &PairState{}
}

func (ps *PairState) GetLastScanTime() string {
	ps.lastScanTimeLock.RLock()
	defer ps.lastScanTimeLock.RUnlock()
	return ps.lastScanTime
}

func (ps *PairState) SetLastScanTime(val string) {
	ps.lastScanTimeLock.Lock()
	defer ps.lastScanTimeLock.Unlock()
	ps.lastScanTime = val
}

func (ps *PairState) GetLastRegime() string {
	ps.lastRegimeLock.RLock()
	defer ps.lastRegimeLock.RUnlock()
	return ps.lastRegime
}

func (ps *PairState) SetLastRegime(val string) {
	ps.lastRegimeLock.Lock()
	defer ps.lastRegimeLock.Unlock()
	ps.lastRegime = val
}

func (ps *PairState) GetLastRecommendation() string {
	ps.lastRecLock.RLock()
	defer ps.lastRecLock.RUnlock()
	return ps.lastRecommendation
}

func (ps *PairState) SetLastRecommendation(val string) {
	ps.lastRecLock.Lock()
	defer ps.lastRecLock.Unlock()
	ps.lastRecommendation = val
}

func (ps *PairState) GetLastConfidence() float64 {
	ps.lastConfLock.RLock()
	defer ps.lastConfLock.RUnlock()
	return ps.lastConfidence
}

func (ps *PairState) SetLastConfidence(val float64) {
	ps.lastConfLock.Lock()
	defer ps.lastConfLock.Unlock()
	ps.lastConfidence = val
}

func (ps *PairState) GetLastRiskLevel() string {
	ps.lastRiskLock.RLock()
	defer ps.lastRiskLock.RUnlock()
	return ps.lastRiskLevel
}

func (ps *PairState) SetLastRiskLevel(val string) {
	ps.lastRiskLock.Lock()
	defer ps.lastRiskLock.Unlock()
	ps.lastRiskLevel = val
}

func (ps *PairState) GetLastReason() string {
	ps.lastReasonLock.RLock()
	defer ps.lastReasonLock.RUnlock()
	return ps.lastReason
}

func (ps *PairState) SetLastReason(val string) {
	ps.lastReasonLock.Lock()
	defer ps.lastReasonLock.Unlock()
	ps.lastReason = val
}

type PairSnapshot struct {
	Pair               string               `json:"pair"`
	Stats              ScannerStatsSnapshot `json:"stats"`
	LastScanTime       string               `json:"last_scan_time"`
	LastRegime         string               `json:"last_regime"`
	LastRecommendation string               `json:"last_recommendation"`
	LastConfidence     float64              `json:"last_confidence"`
	LastRiskLevel      string               `json:"last_risk_level"`
	LastReason         string               `json:"last_reason"`
}

type ScannerState struct {
	pairsMu         sync.RWMutex
	pairs           map[string]*PairState
	pairListMu      sync.RWMutex
	pairList        []string
	decisionsMu     sync.RWMutex
	recentDecisions []RecentDecision
}

func NewScannerState() *ScannerState {
	return &ScannerState{
		pairs: make(map[string]*PairState),
	}
}

func (s *ScannerState) InitializePairs(pairs []string) {
	s.pairsMu.Lock()
	defer s.pairsMu.Unlock()
	s.pairListMu.Lock()
	defer s.pairListMu.Unlock()

	for _, pair := range pairs {
		name := strings.TrimSpace(strings.ToUpper(pair))
		if name == "" {
			continue
		}
		if _, exists := s.pairs[name]; exists {
			continue
		}
		s.pairs[name] = NewPairState()
		s.pairList = append(s.pairList, name)
	}
}

func (s *ScannerState) AddPair(pair string) bool {
	name := strings.TrimSpace(strings.ToUpper(pair))
	if name == "" {
		return false
	}
	s.pairsMu.Lock()
	defer s.pairsMu.Unlock()
	s.pairListMu.Lock()
	defer s.pairListMu.Unlock()

	if _, exists := s.pairs[name]; exists {
		return false
	}
	s.pairs[name] = NewPairState()
	s.pairList = append(s.pairList, name)
	log.Printf("Scanner: added pair %s", name)
	return true
}

func (s *ScannerState) RemovePair(pair string) bool {
	name := strings.TrimSpace(strings.ToUpper(pair))
	s.pairsMu.Lock()
	defer s.pairsMu.Unlock()
	s.pairListMu.Lock()
	defer s.pairListMu.Unlock()

	if _, exists := s.pairs[name]; exists {
		delete(s.pairs, name)
		newList := make([]string, 0, len(s.pairList)-1)
		for _, p := range s.pairList {
			if p != name {
				newList = append(newList, p)
			}
		}
		s.pairList = newList
		log.Printf("Scanner: removed pair %s", name)
		return true
	}
	return false
}

func (s *ScannerState) GetPairs() []string {
	s.pairListMu.RLock()
	defer s.pairListMu.RUnlock()
	res := make([]string, len(s.pairList))
	copy(res, s.pairList)
	return res
}

func (s *ScannerState) GetPairState(pair string) *PairState {
	s.pairsMu.RLock()
	defer s.pairsMu.RUnlock()
	return s.pairs[pair]
}

func (s *ScannerState) AllSnapshots() []PairSnapshot {
	s.pairsMu.RLock()
	defer s.pairsMu.RUnlock()

	var snapshots []PairSnapshot
	for name, ps := range s.pairs {
		snapshots = append(snapshots, PairSnapshot{
			Pair:               name,
			Stats:              ps.Stats.Snapshot(),
			LastScanTime:       ps.GetLastScanTime(),
			LastRegime:         ps.GetLastRegime(),
			LastRecommendation: ps.GetLastRecommendation(),
			LastConfidence:     ps.GetLastConfidence(),
			LastRiskLevel:      ps.GetLastRiskLevel(),
			LastReason:         ps.GetLastReason(),
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Pair < snapshots[j].Pair
	})
	return snapshots
}

func (s *ScannerState) GetRecentDecisions() []RecentDecision {
	s.decisionsMu.RLock()
	defer s.decisionsMu.RUnlock()
	res := make([]RecentDecision, len(s.recentDecisions))
	copy(res, s.recentDecisions)
	return res
}

func (s *ScannerState) AddRecentDecision(decision RecentDecision) {
	s.decisionsMu.Lock()
	defer s.decisionsMu.Unlock()
	s.recentDecisions = append(s.recentDecisions, decision)
	if len(s.recentDecisions) > 50 {
		s.recentDecisions = s.recentDecisions[1:]
	}
}

type StatusTracker interface {
	IsEnabled() bool
	Touch()
}

type executionEngine interface {
	ExecuteBuy(ctx context.Context, pair string, quoteAmount float64, advisory *models.FullBtcAdvisory) (models.ExecutionPlan, error)
	GetAvailableCapital(ctx context.Context, pair string) (float64, error)
}

func isBtcQuotePair(pair string) bool {
	p := strings.ToUpper(pair)
	return strings.HasSuffix(p, "BTC") && p != "BTCUSDT"
}

func Run(
	ctx context.Context,
	state *ScannerState,
	ex exchange.ExchangeClient,
	engine *engine.AdvisoryEngine,
	executor executionEngine,
	mem memory.Store,
	intervalSecs uint64,
	status StatusTracker,
) {
	log.Printf("[%s] Multi-pair scanner started (every %ds)", ex.ExchangeName(), intervalSecs)
	ticker := time.NewTicker(time.Duration(intervalSecs) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Multi-pair scanner stopping", ex.ExchangeName())
			return
		case <-ticker.C:
			if status != nil {
				if !status.IsEnabled() {
					status.Touch()
					log.Printf("[%s] Scanner is disabled/paused, skipping tick", ex.ExchangeName())
					continue
				}
				status.Touch()
			}

			pairs := state.GetPairs()
			if len(pairs) == 0 {
				log.Printf("[%s] Scanner: no pairs configured", ex.ExchangeName())
				continue
			}

			for _, pair := range pairs {
				ps := state.GetPairState(pair)
				if ps != nil {
					scanPair(ctx, state, pair, ps, ex, engine, executor, mem)
					time.Sleep(500 * time.Millisecond)
				}
			}
		}
	}
}

func scanPair(
	ctx context.Context,
	state *ScannerState,
	pair string,
	ps *PairState,
	ex exchange.ExchangeClient,
	engine *engine.AdvisoryEngine,
	executor executionEngine,
	mem memory.Store,
) {
	ps.Stats.Scanned.Add(1)

	nowStr := time.Now().UTC().Format(time.RFC3339)
	ps.SetLastScanTime(nowStr)

	marketData, err := ex.GetMarketData(ctx, pair)
	if err != nil {
		ps.Stats.Errors.Add(1)
		log.Printf("Scanner [%s]: failed to fetch market data: %v", pair, err)
		return
	}

	openOrders, err := ex.GetOpenOrders(ctx, pair)
	if err != nil {
		openOrders = nil
	}

	treasury := mem.GetTreasuryState()

	if treasury.TradingPausedUntil != "" {
		if paused, err := time.Parse(time.RFC3339, treasury.TradingPausedUntil); err == nil {
			if time.Now().UTC().Before(paused.UTC()) {
				log.Printf("Scanner [%s]: skipping (trading paused until %v)", pair, paused)
				return
			}
		}
	}

	config := mem.GetConfig()
	if config.DryRun {
		log.Printf("Scanner [%s]: dry_run mode active", pair)
	}

	lossStreak := treasury.ConsecutiveLosses

	var aiScore *float64
	var riskInfo *models.RiskAssessment
	var pairMetrics *models.PairMetrics

	candles15m, err := ex.GetKlines(ctx, pair, "15m", 200)
	if err == nil && len(candles15m) > 50 {
		candles1h, _ := ex.GetKlines(ctx, pair, "1h", 50)
		candles4h, _ := ex.GetKlines(ctx, pair, "4h", 50)
		btc15m, _ := ex.GetKlines(ctx, "BTCUSDT", "15m", 200)

		metrics := computePairMetrics(candles15m, candles1h, candles4h, btc15m, pair)

		metrics.SpreadPct = (10.0 - marketData.SpreadScore) / 20.0
		metrics.WideSpread = metrics.SpreadPct >= 0.5
		minDepth := marketData.LiquidityScore * 50.0
		metrics.BidDepth = minDepth
		metrics.AskDepth = minDepth
		metrics.LiquidityGrowth = metrics.VolumeGrowth > 0.0

		volEng := engines.VolumeEngine{}
		metrics.WashTradeDetected = volEng.IsWashTrade(&metrics)

		riskManager := engines.RiskManager{}
		drawdownVal := math.Abs(treasury.BtcGrowth7d)
		if drawdownVal > 1.0 {
			drawdownVal = 1.0
		}
		riskAssessment := riskManager.Assess(
			&treasury,
			len(mem.GetPositions()),
			lossStreak,
			drawdownVal,
			treasury.UsdtBalance,
			&config,
		)

		aiScoringEngine := engines.AIScoringEngine{}
		scoring := aiScoringEngine.ScorePair(&metrics, &riskAssessment)

		aiScore = &scoring.Score
		riskInfo = &riskAssessment
		pairMetrics = &metrics
	} else {
		log.Printf("Scanner [%s]: insufficient OHLCV data, using orderbook-only scoring", pair)
	}

	input := models.BtcAdvisoryInput{
		MarketData:     marketData,
		Treasury:       treasury,
		OpenPositions:  openOrders,
		LossStreak:     lossStreak,
		AiScore:        aiScore,
		RiskAssessment: riskInfo,
		PairMetrics:    pairMetrics,
	}

	advisory := engine.Analyze(ctx, &input)

	minConf := config.MinConfidence
	minScore := config.MinScoreThreshold

	if advisory.Recommendation == "APPROVE" &&
		(advisory.Confidence < minConf || advisory.OpportunityScore < minScore) {
		log.Printf("Scanner [%s]: APPROVE blocked — conf %.2f < %.2f OR score %.0f < %.0f",
			pair, advisory.Confidence, minConf, advisory.OpportunityScore, minScore)
		advisory.Recommendation = "MONITOR"
		advisory.Reason = fmt.Sprintf("%s (blocked: conf %.2f < %.2f or score %.0f < %.0f)",
			advisory.Reason, advisory.Confidence, minConf, advisory.OpportunityScore, minScore)
	}

	ps.SetLastRegime(advisory.MarketRegime)
	ps.SetLastRecommendation(advisory.Recommendation)
	ps.SetLastReason(advisory.Reason)
	ps.SetLastConfidence(advisory.Confidence)
	ps.SetLastRiskLevel(advisory.RiskLevel)

	switch advisory.Recommendation {
	case "APPROVE":
		ps.Stats.AdvisoryApprove.Add(1)

		cfg := mem.GetConfig()
		positions := mem.GetPositions()

		canTrade := len(positions) < cfg.MaxPositions && treasury.TradingPausedUntil == ""

		if canTrade {
			capital, err := executor.GetAvailableCapital(ctx, pair)
			if err != nil {
				log.Printf("Scanner [%s]: failed to get capital: %v", pair, err)
				capital = 0.0
			}

			currentPrice, err := ex.GetCurrentPrice(ctx, pair)
			if err != nil {
				currentPrice = 0.0
			}

			quoteAsset := "USDT"
			if isBtcQuotePair(pair) {
				quoteAsset = "BTC"
			}

			clampedSL := advisory.DynamicStopLoss
			closeVal := currentPrice
			riskManager := engines.RiskManager{}

			if pairMetrics != nil {
				if pairMetrics.Close15m > 0.0 {
					closeVal = pairMetrics.Close15m
				}
				clampedSL = riskManager.ClampSl(advisory.DynamicStopLoss, closeVal, pairMetrics.Atr14)
			} else {
				clampedSL = riskManager.MinSlFromAtr(currentPrice, 0.0) // floor only
			}

			if clampedSL != advisory.DynamicStopLoss {
				log.Printf("Scanner [%s]: clamped SL from %.1f%% to %.1f%% (ATR_14=%.6f, close=%.6f)",
					pair, advisory.DynamicStopLoss, clampedSL, pairMetrics.Atr14, closeVal)
				tpSlRatio := 3.0
				if advisory.DynamicStopLoss != 0.0 {
					tpSlRatio = math.Max(advisory.DynamicTakeProfit/math.Abs(advisory.DynamicStopLoss), 2.0)
				}
				advisory.DynamicStopLoss = clampedSL
				advisory.DynamicTakeProfit = math.Abs(clampedSL) * tpSlRatio
				advisory.TpReason = fmt.Sprintf("%s (wider SL due to ATR clamp)", advisory.TpReason)
				advisory.SlReason = fmt.Sprintf("%s (ATR clamp: %.1f%% min)", advisory.SlReason, -clampedSL)
			}

			positionValue := 0.0
			if capital > 0.0 && advisory.DynamicStopLoss < 0.0 {
				positionValue = riskManager.CalcPositionSize(
					capital,
					currentPrice,
					advisory.DynamicStopLoss,
					cfg.RiskPerTradePct,
					cfg.TakerFeePct,
				)
			}

			if positionValue > 0.0 {
				if cfg.DryRun {
					simSize := 0.0
					if currentPrice > 0.0 {
						simSize = positionValue / currentPrice
					}
					monitor.RecordPositionFromAdvisory(mem, &advisory, currentPrice, simSize, pair, "BUY")
					log.Printf("[DRY RUN] Scanner [%s]: APPROVE — simulated BUY of %.2f %s (≈%.8f base) at score %.0f",
						pair, positionValue, quoteAsset, simSize, advisory.OpportunityScore)
				} else {
					plan, err := executor.ExecuteBuy(ctx, pair, positionValue, &advisory)
					if err != nil {
						log.Printf("Scanner [%s]: BUY execution failed: %v", pair, err)
					} else {
						qty := 0.0
						if plan.EntryPrice > 0.0 {
							qty = positionValue / plan.EntryPrice
						}
						log.Printf("Scanner [%s]: BUY executed — %s %.8f (value %.2f %s) (TP:%.1f%%, SL:%.1f%%)",
							pair, plan.Pair, qty, positionValue, quoteAsset, plan.TpPct, plan.SlPct)
					}
				}
			} else {
				log.Printf("Scanner [%s]: APPROVE but zero positionValue computed (capital=%.2f %s)", pair, capital, quoteAsset)
			}
		} else {
			log.Printf("Scanner [%s]: APPROVE blocked — positions=%d/%d paused=%t",
				pair, len(positions), cfg.MaxPositions, treasury.TradingPausedUntil != "")
		}

	case "MONITOR":
		ps.Stats.AdvisoryMonitor.Add(1)
	case "PROTECT_TREASURY", "ENABLE_SAFE_MODE", "REDUCE_EXPOSURE":
		ps.Stats.AdvisoryProtect.Add(1)
	default:
		ps.Stats.AdvisoryReject.Add(1)
	}

	decision := RecentDecision{
		Pair:           pair,
		Timestamp:      nowStr,
		Recommendation: advisory.Recommendation,
		Confidence:     advisory.Confidence,
		RiskLevel:      advisory.RiskLevel,
		Reason:         advisory.Reason,
	}
	state.AddRecentDecision(decision)

	record := models.BtcDecisionRecord{
		Timestamp:      advisory.Timestamp,
		MarketData:     marketData,
		TreasuryBefore: treasury,
		TreasuryAfter:  mem.GetTreasuryState(),
		Advisory:       advisory,
		ActionTaken:    advisory.Recommendation,
	}
	mem.LogDecision(record)

	if advisory.Recommendation != "APPROVE" {
		ts := time.Now().UTC().Format("2006-01-02 15:04")
		lesson := fmt.Sprintf("[%s] [%s] advisory: %s (regime: %s, confidence: %.2f, risk: %s) — %s",
			ts, pair, advisory.Recommendation, advisory.MarketRegime, advisory.Confidence, advisory.RiskLevel, advisory.Reason)
		mem.AddLesson(lesson)
	}
}

func computePairMetrics(
	candles15m []models.Ohlcv,
	candles1h []models.Ohlcv,
	candles4h []models.Ohlcv,
	btc15m []models.Ohlcv,
	pair string,
) models.PairMetrics {
	var close15m, close1h, close4h, close1d float64
	if len(candles15m) > 0 {
		close15m = candles15m[len(candles15m)-1].Close
	}
	if len(candles1h) > 0 {
		close1h = candles1h[len(candles1h)-1].Close
	}
	if len(candles4h) > 0 {
		close4h = candles4h[len(candles4h)-1].Close
	}

	if len(candles4h) >= 7 {
		close1d = candles4h[len(candles4h)-7].Close
	} else {
		close1d = close4h
	}

	var volume15m, volume1h, volume4h, volume1d float64
	if len(candles15m) > 0 {
		volume15m = candles15m[len(candles15m)-1].Volume
	}
	if len(candles1h) > 0 {
		volume1h = candles1h[len(candles1h)-1].Volume
	}
	if len(candles4h) > 0 {
		volume4h = candles4h[len(candles4h)-1].Volume
	}

	limit := 6
	if len(candles4h) < 6 {
		limit = len(candles4h)
	}
	for i := 0; i < limit; i++ {
		volume1d += candles4h[len(candles4h)-1-i].Volume
	}

	atr14 := indicators.ATR(candles15m, 14)
	rsi14 := indicators.RSI(candles15m, 14)
	ema20 := indicators.EMA20(candles15m)
	ema50 := indicators.EMA50(candles15m)
	ema200 := indicators.EMA200(candles15m)
	macdLine, macdSignal, macdHistogram := indicators.MACD(candles15m)
	vwap := indicators.VWAP(candles15m)

	coinRet15m := indicators.ReturnSince(candles15m, 1)
	coinRet1h := indicators.ReturnSince(candles1h, 1)
	coinRet4h := indicators.ReturnSince(candles4h, 1)
	coinRet1d := indicators.ReturnSince(candles4h, 6)

	btcRet15m := indicators.ReturnSince(btc15m, 1)
	btcRet1h := indicators.ReturnSince(btc15m, 4)
	btcRet4h := indicators.ReturnSince(btc15m, 16)
	btcRet1d := indicators.ReturnSince(btc15m, 96)

	rs15m := (coinRet15m - btcRet15m) * 100.0
	rs1h := (coinRet1h - btcRet1h) * 100.0
	rs4h := (coinRet4h - btcRet4h) * 100.0
	rs1d := (coinRet1d - btcRet1d) * 100.0

	rsScore := rs15m*0.10 + rs1h*0.35 + rs4h*0.30 + rs1d*0.25

	volumeGrowth := indicators.VolumeGrowth(candles15m, 20)

	var atrExpansion float64
	if atr14 > 0.0 && len(candles15m) > 1 {
		prevAtr := indicators.ATR(candles15m[:len(candles15m)-1], 14)
		if prevAtr > 0.0 {
			atrExpansion = (atr14 - prevAtr) / prevAtr
		}
	}

	emaBullishAlignment := ema20 > ema50 && ema50 > ema200
	macdBullish := macdLine > macdSignal && macdHistogram > 0.0
	volumeSpike := volumeGrowth > 1.0
	volumeExpansion := indicators.IsVolumeExpansion(candles15m, candles1h, candles4h)

	zeroVolCount := 0
	checkLimit := 10
	if len(candles15m) < 10 {
		checkLimit = len(candles15m)
	}
	for i := 0; i < checkLimit; i++ {
		if candles15m[len(candles15m)-1-i].Volume == 0.0 {
			zeroVolCount++
		}
	}
	lowLiquidity := zeroVolCount >= 3

	return models.PairMetrics{
		Pair:                pair,
		Close15m:            close15m,
		Close1h:             close1h,
		Close4h:             close4h,
		Close1d:             close1d,
		Volume15m:           volume15m,
		Volume1h:            volume1h,
		Volume4h:            volume4h,
		Volume1d:            volume1d,
		Atr14:               atr14,
		AtrAtr:              atr14,
		Rsi14:               rsi14,
		Ema20:               ema20,
		Ema50:               ema50,
		Ema200:              ema200,
		MacdLine:            macdLine,
		MacdSignal:          macdSignal,
		MacdHistogram:       macdHistogram,
		Vwap:                vwap,
		BtcReturn15m:        btcRet15m,
		BtcReturn1h:         btcRet1h,
		BtcReturn4h:         btcRet4h,
		BtcReturn1d:         btcRet1d,
		Rs15m:               rs15m,
		Rs1h:                rs1h,
		Rs4h:                rs4h,
		Rs1d:                rs1d,
		RsScore:             rsScore,
		VolumeGrowth:        volumeGrowth,
		AtrExpansion:        atrExpansion,
		EmaBullishAlignment: emaBullishAlignment,
		MacdBullish:         macdBullish,
		VolumeSpike:         volumeSpike,
		VolumeExpansion:     volumeExpansion,
		LiquidityGrowth:     volumeGrowth > 0.0,
		LowLiquidity:        lowLiquidity,
	}
}
