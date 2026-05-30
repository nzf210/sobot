package orchestrator

import (
    "fmt"
    "time"

    "hybrid-solana-bot/internal/config"
    "hybrid-solana-bot/internal/executor"
    "hybrid-solana-bot/internal/llm"
    "hybrid-solana-bot/internal/memory"
    "hybrid-solana-bot/internal/models"
    "hybrid-solana-bot/internal/risk"
    "hybrid-solana-bot/internal/scoring"
)

type Orchestrator struct {
    riskEngine *risk.RiskEngine
    cfg        config.Config
    mem        *memory.MemoryStore
}

func New(cfg config.Config, mem *memory.MemoryStore) *Orchestrator {
    return &Orchestrator{
        riskEngine: risk.New(mem),
        cfg:        cfg,
        mem:        mem,
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

    // Load context from memory files
    ctxStr := o.mem.LoadContext()

    llmResult, err := llm.Analyze(o.cfg, metrics, ctxStr)
    if err != nil {
        return map[string]interface{}{
            "status": "error",
            "error": err.Error(),
        }
    }

    // Log the decision
    o.mem.LogDecision(metrics.Token, metrics, llmResult.Decision, fmt.Sprintf("Confidence: %.2f", llmResult.Confidence))

    if llmResult.Decision != "SELL" && llmResult.Decision != "HOLD" {
        // Trigger Executor
        go func() {
            userCfg := o.mem.GetUserConfig()
            solAmount := userCfg.MaxDeployAmountSol
            lamports := int64(solAmount * 1e9)
            
            resp, err := executor.ExecuteSwap("So11111111111111111111111111111111111111112", metrics.Token, lamports)
            if err == nil && resp != nil && resp.Success {
                // Record the new position to memory
                positions := o.mem.GetPositions()
                newPos := models.Position{
                    TokenAddress: metrics.Token,
                    EntryPrice:   metrics.PriceSOL,
                    EntryAmount:  solAmount,
                    AmountToken:  0, // Would need actual token amount received in a prod scenario
                    EntryTime:    time.Now().UTC(),
                    IsClosed:     false,
                }
                positions = append(positions, newPos)
                o.mem.SavePositions(positions)
            }
        }()
    }

    return map[string]interface{}{
        "status": "approved",
        "score": score,
        "llm": llmResult,
    }
}