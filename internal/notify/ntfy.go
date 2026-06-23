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

// ntfyProvider publishes to an ntfy server (https://ntfy.sh or self-hosted)
// using the JSON publish API, which lets us attach structured action buttons
// without the escaping pitfalls of the header-based format.
type ntfyProvider struct {
	client *http.Client
	server string // base URL, e.g. https://ntfy.sh
	topic  string // bare topic or full topic URL
	token  string // optional access token for protected topics
}

func (p *ntfyProvider) Name() string { return "ntfy" }

// ntfyAction mirrors ntfy's JSON action schema.
type ntfyAction struct {
	Action  string            `json:"action"` // "view" | "http" | "broadcast"
	Label   string            `json:"label"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Clear   bool              `json:"clear,omitempty"`
}

type ntfyPayload struct {
	Topic    string       `json:"topic"`
	Title    string       `json:"title,omitempty"`
	Message  string       `json:"message,omitempty"`
	Priority int          `json:"priority,omitempty"`
	Tags     []string     `json:"tags,omitempty"`
	Click    string       `json:"click,omitempty"`
	Actions  []ntfyAction `json:"actions,omitempty"`
}

// resolve splits the configured server/topic into the JSON publish endpoint and
// the bare topic name. A full topic URL in `topic` (e.g.
// "https://ntfy.sh/mytopic") overrides `server`.
func (p *ntfyProvider) resolve() (endpoint, topic string) {
	server := strings.TrimRight(p.server, "/")
	topic = p.topic
	if strings.HasPrefix(topic, "http://") || strings.HasPrefix(topic, "https://") {
		trimmed := strings.TrimRight(topic, "/")
		if idx := strings.LastIndex(trimmed, "/"); idx != -1 {
			server = trimmed[:idx]
			topic = trimmed[idx+1:]
		}
	}
	// ntfy's JSON publish endpoint is the server root.
	return server, topic
}

func (p *ntfyProvider) Send(ctx context.Context, msg Message) error {
	endpoint, topic := p.resolve()

	payload := ntfyPayload{
		Topic:    topic,
		Title:    msg.Title,
		Message:  msg.Body,
		Priority: msg.Priority,
		Tags:     msg.Tags,
		Click:    msg.ClickURL,
	}
	for _, a := range msg.Actions {
		action := "view"
		if a.Type == "http" {
			action = "http"
		}
		payload.Actions = append(payload.Actions, ntfyAction{
			Action:  action,
			Label:   a.Label,
			URL:     a.URL,
			Method:  a.Method,
			Headers: a.Headers,
			Body:    a.Body,
			Clear:   a.Clear,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal ntfy payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post to ntfy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
