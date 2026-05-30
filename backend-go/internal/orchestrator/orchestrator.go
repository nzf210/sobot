package orchestrator

import (
    "hybrid-solana-bot/internal/llm"
    "hybrid-solana-bot/internal/models"
    "hybrid-solana-bot/internal/risk"
    "hybrid-solana-bot/internal/scoring"
)

type Orchestrator struct {
    riskEngine *risk.RiskEngine
}

func New() *Orchestrator {
    return &Orchestrator{
        riskEngine: risk.New(),
    }
}

func (o *Orchestrator) Process(metrics models.TokenMetrics) interface{} {

    if !o.riskEngine.Validate(metrics) {
        return map[string]interface{}{
            "status": "rejected",
        }
    }

    score := scoring.Compute(metrics)

    if score < 0.5 {
        return map[string]interface{}{
            "status": "low_score",
        }
    }

    llmResult := llm.Analyze()

    return map[string]interface{}{
        "status": "approved",
        "score": score,
        "llm": llmResult,
    }
}