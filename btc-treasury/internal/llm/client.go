package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"btc-treasury/internal/models"
)

type LlmClient struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

func NewLlmClient(url, model, apiKey string) *LlmClient {
	return &LlmClient{
		url:    strings.TrimSuffix(url, "/"),
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llmChoice struct {
	Message llmMessage `json:"message"`
}

type llmResponse struct {
	Choices []llmChoice `json:"choices"`
}

type advisoryJSON struct {
	Recommendation    string   `json:"recommendation"`
	Confidence        float64  `json:"confidence"`
	RiskLevel         string   `json:"risk_level"`
	TreasuryMode      string   `json:"treasury_mode"`
	Reason            string   `json:"reason"`
	Warnings          []string `json:"warnings"`
	MarketRegime      string   `json:"market_regime"`
	OpportunityScore  float64  `json:"opportunity_score"`
	DynamicTakeProfit float64  `json:"dynamic_take_profit"`
	DynamicStopLoss   float64  `json:"dynamic_stop_loss"`
	TpReason          string   `json:"tp_reason"`
	SlReason          string   `json:"sl_reason"`
}

func stripCodeFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```") {
		rest := strings.TrimPrefix(trimmed, "```")
		if idx := strings.Index(rest, "\n"); idx != -1 {
			rest = rest[idx+1:]
		}
		rest = strings.TrimSpace(rest)
		if strings.HasSuffix(rest, "```") {
			rest = strings.TrimSuffix(rest, "```")
		}
		return strings.TrimSpace(rest)
	}
	return trimmed
}

func extractJSONObject(s string) (string, bool) {
	start := strings.Index(s, "{")
	if start == -1 {
		return "", false
	}
	depth := 0
	end := start
	runes := []rune(s)
	for i := start; i < len(runes); i++ {
		ch := runes[i]
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if depth == 0 && end >= start {
		return string(runes[start : end+1]), true
	}
	return "", false
}

func (c *LlmClient) Call(ctx context.Context, systemPrompt, userPrompt string) (models.FullBtcAdvisory, error) {
	bodyMap := map[string]interface{}{
		"model": c.model,
		"messages": []llmMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		"temperature": 0.373,
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return models.FullBtcAdvisory{}, err
	}

	reqURL := fmt.Sprintf("%s/chat/completions", c.url)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return models.FullBtcAdvisory{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return models.FullBtcAdvisory{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return models.FullBtcAdvisory{}, fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, string(body))
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.FullBtcAdvisory{}, err
	}
	if len(strings.TrimSpace(string(rawBody))) == 0 {
		return models.FullBtcAdvisory{}, errors.New("LLM API returned an empty response body")
	}

	var response llmResponse
	if err := json.Unmarshal(rawBody, &response); err != nil {
		limit := len(rawBody)
		if limit > 500 {
			limit = 500
		}
		return models.FullBtcAdvisory{}, fmt.Errorf("failed to parse LLM envelope JSON: %w — raw: %s", err, string(rawBody[:limit]))
	}

	if len(response.Choices) == 0 {
		return models.FullBtcAdvisory{}, errors.New("no choices in LLM response")
	}

	content := response.Choices[0].Message.Content
	if len(strings.TrimSpace(content)) == 0 {
		return models.FullBtcAdvisory{}, errors.New("LLM returned an empty content field")
	}

	stripped := stripCodeFence(content)
	jsonStr, ok := extractJSONObject(stripped)
	if !ok {
		jsonStr, ok = extractJSONObject(content)
		if !ok {
			jsonStr = stripped
		}
	}

	var result advisoryJSON
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		limit := len(content)
		if limit > 500 {
			limit = 500
		}
		return models.FullBtcAdvisory{}, fmt.Errorf("failed to parse AdvisoryJson: %w — content: %s", err, content[:limit])
	}

	return models.FullBtcAdvisory{
		Recommendation:    result.Recommendation,
		Confidence:        result.Confidence,
		RiskLevel:         result.RiskLevel,
		TreasuryMode:      result.TreasuryMode,
		Reason:            result.Reason,
		Warnings:          result.Warnings,
		MarketRegime:      result.MarketRegime,
		OpportunityScore:  result.OpportunityScore,
		BypassQuant:       true,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		DynamicTakeProfit: result.DynamicTakeProfit,
		DynamicStopLoss:   result.DynamicStopLoss,
		TpReason:          result.TpReason,
		SlReason:          result.SlReason,
	}, nil
}
