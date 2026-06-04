package runtime

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"btc-treasury/internal/config"
	"btc-treasury/internal/engine"
	"btc-treasury/internal/exchange"
	"btc-treasury/internal/execution"
	"btc-treasury/internal/memory"
	"btc-treasury/internal/monitor"
	"btc-treasury/internal/scanner"
)

type AccountStatus struct {
	lastHeartbeatUnix atomic.Int64
	restartCount      atomic.Uint32
	enabled           atomic.Bool
}

func NewAccountStatus(enabled bool) *AccountStatus {
	s := &AccountStatus{}
	s.enabled.Store(enabled)
	return s
}

func (s *AccountStatus) IsEnabled() bool {
	return s.enabled.Load()
}

func (s *AccountStatus) SetEnabled(enabled bool) {
	s.enabled.Store(enabled)
}

func (s *AccountStatus) Touch() {
	s.lastHeartbeatUnix.Store(time.Now().Unix())
}

func (s *AccountStatus) HeartbeatUnix() int64 {
	return s.lastHeartbeatUnix.Load()
}

func (s *AccountStatus) Restarts() uint32 {
	return s.restartCount.Load()
}

func (s *AccountStatus) IncrementRestart() {
	s.restartCount.Add(1)
}

type AccountRuntime struct {
	Key          exchange.AccountKey
	Spec         config.AccountSpec
	AccountID    string
	Exchange     exchange.ExchangeClient
	Mem          memory.Store
	ScannerState *scanner.ScannerState
	Executor     *execution.ExecutionEngine
	Engine       *engine.AdvisoryEngine
	Status       *AccountStatus
}

func Build(
	spec *config.AccountSpec,
	ex exchange.ExchangeClient,
	mem memory.Store,
	llmURL string,
	llmModel string,
	llmAPIKey string,
) *AccountRuntime {
	scannerState := scanner.NewScannerState()
	executor := execution.NewExecutionEngine(ex, mem)
	advisoryEngine := engine.NewAdvisoryEngine(llmURL, llmModel, llmAPIKey, mem)

	key := exchange.AccountKeyFromSpec(spec)
	status := NewAccountStatus(spec.Enabled)

	return &AccountRuntime{
		Key:          key,
		Spec:         *spec,
		AccountID:    spec.ID,
		Exchange:     ex,
		Mem:          mem,
		ScannerState: scannerState,
		Executor:     executor,
		Engine:       advisoryEngine,
		Status:       status,
	}
}

func (r *AccountRuntime) InitializePairs(ctx context.Context) {
	var pairs []string
	if len(r.Spec.ScannerPairs) == 0 {
		pairs = r.Mem.GetConfig().ScannerPairs
	} else {
		pairs = make([]string, len(r.Spec.ScannerPairs))
		copy(pairs, r.Spec.ScannerPairs)
	}

	r.ScannerState.InitializePairs(pairs)

	savedCfg := r.Mem.GetConfig()
	savedCfg.ScannerPairs = pairs
	r.Mem.SaveConfig(savedCfg)
}

func (r *AccountRuntime) BuildMonitor() *monitor.PositionMonitor {
	label := fmt.Sprintf("%s/%s", string(r.Spec.Exchange), r.AccountID)
	return monitor.NewPositionMonitor(r.Mem, r.Exchange, r.Status).WithLabel(label)
}
