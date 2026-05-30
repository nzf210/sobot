package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type SwapRequest struct {
	InputMint  string `json:"inputMint"`
	OutputMint string `json:"outputMint"`
	Amount     int64  `json:"amount"`
}

type SwapResponse struct {
	Success bool `json:"success"`
	Result  struct {
		TxHash string `json:"txHash"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"result"`
}

func ExecuteSwap(inputMint, outputMint string, amount int64) (*SwapResponse, error) {
	host := os.Getenv("EXECUTOR_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("EXECUTOR_PORT")
	if port == "" {
		port = "3000"
	}

	url := fmt.Sprintf("http://%s:%s/execute", host, port)

	reqBody := SwapRequest{
		InputMint:  inputMint,
		OutputMint: outputMint,
		Amount:     amount,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res SwapResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}
