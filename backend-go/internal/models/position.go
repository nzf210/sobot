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
}
