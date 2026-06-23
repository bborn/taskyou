package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// telegramProvider delivers via the Telegram Bot API. Telegram inline buttons
// can only navigate (URL buttons) — they can't POST to our API the way an ntfy
// "http" action can — so actions degrade to deep links that open the web UI.
type telegramProvider struct {
	client *http.Client
	token  string
	chatID string
	// apiBase is the Telegram Bot API root. Empty defaults to the public API;
	// overridable in tests.
	apiBase string
}

func (p *telegramProvider) Name() string { return "telegram" }

func (p *telegramProvider) base() string {
	if p.apiBase != "" {
		return strings.TrimRight(p.apiBase, "/")
	}
	return "https://api.telegram.org"
}

type telegramButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type telegramMarkup struct {
	InlineKeyboard [][]telegramButton `json:"inline_keyboard"`
}

type telegramPayload struct {
	ChatID      string          `json:"chat_id"`
	Text        string          `json:"text"`
	ReplyMarkup *telegramMarkup `json:"reply_markup,omitempty"`
}

func (p *telegramProvider) Send(ctx context.Context, msg Message) error {
	var sb strings.Builder
	if msg.Title != "" {
		sb.WriteString(msg.Title)
	}
	if msg.Body != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(msg.Body)
	}

	payload := telegramPayload{
		ChatID: p.chatID,
		Text:   sb.String(),
	}

	// Telegram can't POST, so only URL-navigation buttons survive. Always offer
	// "Open task" so the user can reply from the web UI.
	var buttons []telegramButton
	for _, a := range msg.Actions {
		if a.Type == "view" && a.URL != "" {
			buttons = append(buttons, telegramButton{Text: a.Label, URL: a.URL})
		}
	}
	if len(buttons) == 0 && msg.ClickURL != "" {
		buttons = append(buttons, telegramButton{Text: "Open task", URL: msg.ClickURL})
	}
	if len(buttons) > 0 {
		payload.ReplyMarkup = &telegramMarkup{InlineKeyboard: [][]telegramButton{buttons}}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", p.base(), p.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post to telegram: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
