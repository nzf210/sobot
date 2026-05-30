package models

import "time"

type Position struct {
	TokenAddress string    `json:"token_address"`
	EntryPrice   float64   `json:"entry_price"`
	EntryAmount  float64   `json:"entry_amount"` // SOL
	AmountToken  float64   `json:"amount_token"` // token
	EntryTime    time.Time `json:"entry_time"`
	IsClosed     bool      `json:"is_closed"`
}
