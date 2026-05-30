package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"hybrid-solana-bot/internal/config"
	"hybrid-solana-bot/internal/models"
)

type Response struct {
	Decision        string  `json:"decision"`
	Confidence      float64 `json:"confidence"`
	NarrativeScore  float64 `json:"narrative_score"`
	DLMMSuitability float64 `json:"dlmm_suitability"`
}

type openAIRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func Analyze(cfg config.Config, metrics models.TokenMetrics, ctxStr string) (Response, error) {
	if !cfg.LLMEnabled || cfg.LLMAPIKey == "" {
		// Return heuristic fallback if LLM is disabled or no key is provided
		return Response{
			Decision:        "MICRO_ENTRY_ONLY",
			Confidence:      0.78,
			NarrativeScore:  0.72,
			DLMMSuitability: 0.61,
		}, nil
	}

	prompt := fmt.Sprintf(`Analyze the following token metrics and output ONLY a JSON object:
{
  "decision": "string (BUY, SELL, HOLD, MICRO_ENTRY_ONLY)",
  "confidence": "number between 0 and 1",
  "narrative_score": "number between 0 and 1",
  "dlmm_suitability": "number between 0 and 1"
}

Bot Memory Context (Use this to adapt your strategy):
%s

Metrics:
Token: %s
Liquidity (SOL): %.2f
Market Cap (SOL): %.2f
Volume 5m (SOL): %.2f
Buy/Sell Ratio: %.2f
Organic Score: %.2f
Wash Trade Probability: %.2f`, 
		ctxStr, metrics.Token, metrics.LiquiditySOL, metrics.MarketCapSOL, metrics.Volume5mSOL, 
		metrics.BuySellRatio, metrics.OrganicScore, metrics.WashTradeProbability)

	reqBody := openAIRequest{
		Model: cfg.LLMModel,
		Messages: []message{
			{Role: "system", Content: "You are a crypto trading AI. Output strictly valid JSON."},
			{Role: "user", Content: prompt},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Response{}, err
	}

	url := fmt.Sprintf("%s/chat/completions", cfg.LLMURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return Response{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.LLMAPIKey))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("LLM API returned status: %s, body: %s", resp.Status, string(bodyBytes))
	}

	var aiResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		return Response{}, err
	}

	if len(aiResp.Choices) == 0 {
		return Response{}, fmt.Errorf("no response from LLM")
	}

	// Try parsing JSON out of the response
	content := aiResp.Choices[0].Message.Content
	var parsed Response
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// If LLM returned text around JSON, could use regex, but we instructed to return strictly JSON
		return Response{}, fmt.Errorf("failed to parse LLM JSON: %v", err)
	}

	return parsed, nil
}