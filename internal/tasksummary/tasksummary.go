package tasksummary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

const (
	summaryModel     = "claude-haiku-4-5-20251001"
	summaryMaxTokens = 180
	maxLogLines      = 160
	maxLogChars      = 12000
	maxLineChars     = 300
)

type Service struct {
	apiKey     string
	httpClient *http.Client
}

func NewService(apiKey string) *Service {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Service{
		apiKey: apiKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   25 * time.Second,
		},
	}
}

func (s *Service) IsAvailable() bool {
	return s.apiKey != ""
}

func GenerateAndStore(ctx context.Context, database *db.DB, taskID int64) (string, error) {
	task, err := database.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", fmt.Errorf("task not found")
	}
	if strings.TrimSpace(task.Summary) != "" {
		return task.Summary, nil
	}

	apiKey, _ := database.GetSetting("anthropic_api_key")
	svc := NewService(apiKey)
	if !svc.IsAvailable() {
		return "", fmt.Errorf("no API key available")
	}

	logs, _ := database.GetTaskLogs(taskID, maxLogLines)
	summary, err := svc.GenerateSummary(ctx, task, logs)
	if err != nil {
		return "", err
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", fmt.Errorf("summary was empty")
	}

	if err := database.UpdateTaskSummary(taskID, summary); err != nil {
		return "", err
	}

	return summary, nil
}

func (s *Service) GenerateSummary(ctx context.Context, task *db.Task, logs []*db.TaskLog) (string, error) {
	if task == nil {
		return "", fmt.Errorf("task is nil")
	}
	if s.apiKey == "" {
		return "", fmt.Errorf("no API key available")
	}

	prompt := buildSummaryPrompt(task, logs)
	return s.callAPI(ctx, prompt)
}

type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Message string `json:"message"`
}

func (s *Service) callAPI(ctx context.Context, prompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     summaryModel,
		MaxTokens: summaryMaxTokens,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return apiResp.Content[0].Text, nil
}

func buildSummaryPrompt(task *db.Task, logs []*db.TaskLog) string {
	var sb strings.Builder
	sb.WriteString("Summarize the task activity for a user who is context switching.\n")
	sb.WriteString("Output 2-4 short bullet points starting with '-'.\n")
	sb.WriteString("Include: the user's request (from title/body), key actions by the agent, and outcome/next step if visible.\n")
	sb.WriteString("Be concise, avoid speculation, and output ONLY the bullets.\n\n")

	sb.WriteString("Task:\n")
	sb.WriteString(fmt.Sprintf("Title: %s\n", task.Title))
	if body := strings.TrimSpace(task.Body); body != "" {
		if len(body) > 1200 {
			body = body[:1200] + "..."
		}
		sb.WriteString("Body:\n")
		sb.WriteString(body)
		sb.WriteString("\n")
	}
	if task.Status != "" {
		sb.WriteString(fmt.Sprintf("Status: %s\n", task.Status))
	}

	logText := formatLogs(logs)
	if logText != "" {
		sb.WriteString("\nRecent activity logs (most recent last):\n")
		sb.WriteString(logText)
	}

	return sb.String()
}

func formatLogs(logs []*db.TaskLog) string {
	if len(logs) == 0 {
		return ""
	}

	if len(logs) > maxLogLines {
		logs = logs[:maxLogLines]
	}

	// Logs are newest-first; reverse for chronological order.
	reversed := make([]*db.TaskLog, len(logs))
	for i, log := range logs {
		reversed[len(logs)-1-i] = log
	}

	var sb strings.Builder
	chars := 0
	for _, log := range reversed {
		content := strings.TrimSpace(log.Content)
		if content == "" {
			continue
		}
		if len(content) > maxLineChars {
			content = content[:maxLineChars] + "..."
		}
		line := fmt.Sprintf("[%s] %s: %s", log.CreatedAt.Format("15:04"), log.LineType, content)
		if chars+len(line)+1 > maxLogChars {
			break
		}
		sb.WriteString(line)
		sb.WriteString("\n")
		chars += len(line) + 1
	}

	return strings.TrimSpace(sb.String())
}
