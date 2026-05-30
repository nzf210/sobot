package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"

	"hybrid-solana-bot/internal/engines"
)

// PipelineExecutor handles the actual trade execution via the TS executor service.
type PipelineExecutor struct {
	executorURL string
	log         *zap.Logger
}

// NewPipelineExecutor creates a new executor client for the TS service.
func NewPipelineExecutor(executorHost, executorPort string, log *zap.Logger) *PipelineExecutor {
	return &PipelineExecutor{
		executorURL: fmt.Sprintf("http://%s:%s", executorHost, executorPort),
		log:         log,
	}
}

// ExecuteBuy buys a token based on the pipeline signal.
func (e *PipelineExecutor) ExecuteBuy(sig *engines.PipelineSignal) (string, error) {
	solAmount := sig.RecommendedSizeSOL
	lamports := int64(solAmount * 1e9)

	return e.executeSwap("So11111111111111111111111111111111111111112", sig.Metrics.Token, lamports)
}

// ExecuteSell sells a token based on the pipeline signal.
func (e *PipelineExecutor) ExecuteSell(sig *engines.PipelineSignal, amountToken float64) (string, error) {
	lamports := int64(amountToken * 1e6)
	return e.executeSwap(sig.Metrics.Token, "So11111111111111111111111111111111111111112", lamports)
}

func (e *PipelineExecutor) executeSwap(inputMint, outputMint string, amount int64) (string, error) {
	url := fmt.Sprintf("%s/execute", e.executorURL)

	reqBody := SwapRequest{
		InputMint:  inputMint,
		OutputMint: outputMint,
		Amount:     amount,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("EXECUTOR_API_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("executor service unreachable: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("executor returned %d: %s", resp.StatusCode, string(body))
	}

	var res SwapResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if !res.Success {
		return "", fmt.Errorf("swap failed: %s", res.Result.Error)
	}

	e.log.Info("Swap executed successfully",
		zap.String("txHash", res.Result.TxHash),
		zap.String("inputMint", inputMint),
		zap.String("outputMint", outputMint),
		zap.Int64("amount", amount),
	)

	return res.Result.TxHash, nil
}

// GetWalletBalance fetches the current wallet balance from the executor service.
func (e *PipelineExecutor) GetWalletBalance() (*WalletResponse, error) {
	url := fmt.Sprintf("%s/wallet", e.executorURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	if key := os.Getenv("EXECUTOR_API_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executor service unreachable: %v", err)
	}
	defer resp.Body.Close()

	var res WalletResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}

// DeployDLMM deploys a Dynamic Liquidity Market Maker position.
func (e *PipelineExecutor) DeployDLMM(sig *engines.PipelineSignal) (string, error) {
	url := fmt.Sprintf("%s/deploy-dlmm", e.executorURL)

	reqBody := map[string]interface{}{
		"tokenAddress":     sig.Metrics.Token,
		"liquiditySOL":    sig.Metrics.LiquiditySOL,
		"confidenceScore": sig.ConfidenceScore,
		"dlmmSuitability": sig.LLMDLMMSuitability,
		"recommendedSize": sig.RecommendedSizeSOL,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("EXECUTOR_API_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("DLMM deploy service unreachable: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success  bool   `json:"success"`
		Position string `json:"position"`
		Error    string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if !result.Success {
		return "", fmt.Errorf("DLMM deployment failed: %s", result.Error)
	}

	e.log.Info("DLMM position deployed", zap.String("position", result.Position))
	return result.Position, nil
}
