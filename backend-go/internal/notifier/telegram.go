package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type TelegramNotifier struct {
	botToken string
	chatIDs  []string
}

func NewTelegramNotifier(botToken string, chatIDs []string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatIDs:  chatIDs,
	}
}

func (t *TelegramNotifier) SendMessage(message string) error {
	if t.botToken == "" || len(t.chatIDs) == 0 {
		return fmt.Errorf("telegram credentials not configured")
	}

	var errs []error
	for _, chatID := range t.chatIDs {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)
		
		payload := map[string]string{
			"chat_id": chatID,
			"text":    message,
			"parse_mode": "Markdown",
		}
		
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		
		if resp.StatusCode != http.StatusOK {
			errs = append(errs, fmt.Errorf("failed to send telegram message to %s, status: %d", chatID, resp.StatusCode))
		}
		resp.Body.Close()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors sending messages: %v", errs)
	}

	return nil
}
