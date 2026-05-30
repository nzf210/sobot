package orchestrator

import (
	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/engines"
	"hybrid-solana-bot/internal/memory"
	"hybrid-solana-bot/internal/models"

	"go.uber.org/zap"
)

// Orchestrator is the legacy orchestrator kept for backward compatibility.
// Use PipelineOrchestrator for the full pipeline.
type Orchestrator struct {
	pipeline *PipelineOrchestrator
	cfg      config.Config
	mem      *memory.MemoryStore
	log      *zap.Logger
}

func New(cfg config.Config, mem *memory.MemoryStore) *Orchestrator {
	// Note: logger is nil here, it will be set by the server
	return &Orchestrator{
		cfg: cfg,
		mem: mem,
	}
}

func (o *Orchestrator) SetLogger(log *zap.Logger) {
	o.log = log
	o.pipeline = NewPipeline(o.cfg, o.mem, log)
}

// Process runs the full pipeline and returns the signal result.
func (o *Orchestrator) Process(metrics models.TokenMetrics) *engines.PipelineSignal {
	if o.pipeline == nil {
		o.SetLogger(o.log)
	}
	return o.pipeline.Process(metrics)
}

// GetPipeline returns the underlying pipeline for direct access.
func (o *Orchestrator) GetPipeline() *PipelineOrchestrator {
	return o.pipeline
}