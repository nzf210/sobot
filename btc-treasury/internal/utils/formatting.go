package utils

import (
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// EscapeMdv2 escapes all characters that Telegram's MarkdownV2 parser treats as special.
// Characters: _ * [ ] ( ) ~ ` > # + - = | { } . ! \
func EscapeMdv2(s string) string {
	// Order matters: escape backslash first
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}

// ChunkText splits text into chunks each <= maxLen chars (runes), breaking on newlines
// whenever possible. This prevents splitting inside a MarkdownV2 entity.
func ChunkText(text string, maxLen int) []string {
	runeCount := utf8.RuneCountInString(text)
	if runeCount <= maxLen {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	var currentLen int

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		lineRuneCount := utf8.RuneCountInString(line)
		
		needed := lineRuneCount
		if currentLen > 0 {
			needed = currentLen + 1 + lineRuneCount
		}

		if needed > maxLen && currentLen > 0 {
			// Flush current chunk
			chunks = append(chunks, current.String())
			current.Reset()
			currentLen = 0
		}

		// Edge case: a single line that is itself longer than maxLen
		if lineRuneCount > maxLen {
			if currentLen > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
				currentLen = 0
			}

			runes := []rune(line)
			for i := 0; i < len(runes); i += maxLen {
				end := i + maxLen
				if end > len(runes) {
					end = len(runes)
				}
				chunks = append(chunks, string(runes[i:end]))
			}
			continue
		}

		if currentLen > 0 {
			current.WriteRune('\n')
			currentLen++
		}
		current.WriteString(line)
		currentLen += lineRuneCount
	}

	if currentLen > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

// SendMdv2Safe sends a message with MarkdownV2 formatting. If the message exceeds
// Telegram's 4096-char limit it is automatically split into multiple messages.
// If MarkdownV2 parsing fails, it falls back to plain text.
func SendMdv2Safe(bot *tgbotapi.BotAPI, chatID int64, text string) (*tgbotapi.Message, error) {
	const maxTgLen = 4000
	chunks := ChunkText(text, maxTgLen)
	total := len(chunks)

	var lastMsg *tgbotapi.Message
	for i, chunk := range chunks {
		body := chunk
		if total > 1 {
			body = fmt.Sprintf("_%d/%d_\n%s", i+1, total, chunk)
		}

		msg := tgbotapi.NewMessage(chatID, body)
		msg.ParseMode = tgbotapi.ModeMarkdownV2

		m, err := bot.Send(msg)
		if err != nil {
			log.Printf("MarkdownV2 send failed (chunk %d/%d, falling back to plain text): %v", i+1, total, err)
			fallbackMsg := tgbotapi.NewMessage(chatID, chunk)
			m2, err2 := bot.Send(fallbackMsg)
			if err2 != nil {
				return nil, err2
			}
			m = m2
		}
		lastMsg = &m
	}

	if lastMsg == nil {
		return nil, fmt.Errorf("chunk_text produced zero chunks for text of length %d", len(text))
	}
	return lastMsg, nil
}
