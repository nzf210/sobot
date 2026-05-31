package models

import "time"

type Position struct {
	TokenAddress    string    `json:"token_address"`
	EntryPrice      float64   `json:"entry_price"`
	EntryAmount     float64   `json:"entry_amount"` // SOL
	AmountToken     float64   `json:"amount_token"` // token
	EntryTime       time.Time `json:"entry_time"`
	HighestPrice    float64   `json:"highest_price"`
	LowestPrice     float64   `json:"lowest_price"`
	ExitPrice       float64   `json:"exit_price"`
	ExitTime        time.Time `json:"exit_time"`
	ExitAmount      float64   `json:"exit_amount"` // SOL value at exit
	ProfitLossUsd   float64   `json:"profit_loss_usd"`
	IsClosed        bool      `json:"is_closed"`
	// Dynamic TP/SL set by LLM at entry time
	TakeProfitPct   float64   `json:"take_profit_pct"`   // override config if set (>0)
	StopLossPct     float64   `json:"stop_loss_pct"`     // override config if set (<0)
	TrailingTPPct   float64   `json:"trailing_tp_pct"`   // trailing TP percentage
	UseTrailing     bool      `json:"use_trailing"`      // use smart trailing
	LLMTPReason     string    `json:"llm_tp_reason"`     // LLM reasoning for TP
	LLMSLReason     string    `json:"llm_sl_reason"`     // LLM reasoning for SL
	LLMConfidence   float64   `json:"llm_confidence"`     // LLM confidence at entry
}
