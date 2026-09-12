package notify

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
)

// Telegram posts a message via the Bot API. No-op if TELEGRAM_BOT_TOKEN or
// TELEGRAM_CHAT_ID isn't set, so it's opt-in and never blocks the desktop
// notification path.
func Telegram(title, body string) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		return nil
	}
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	resp, err := http.PostForm(api, url.Values{
		"chat_id": {chatID},
		"text":    {title + "\n" + body},
	})
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: status %s", resp.Status)
	}
	return nil
}
