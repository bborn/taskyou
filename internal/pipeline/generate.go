package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GenerateDefinition turns a free-text description of a workflow into a validated
// Definition (and its YAML), using Claude. It's the engine behind
// `ty workflow new "<describe it>"`: the user describes the flow in English and
// gets a ready-to-edit workflow file, instead of authoring YAML by hand.
func GenerateDefinition(ctx context.Context, apiKey, description string) (Definition, []byte, error) {
	if strings.TrimSpace(apiKey) == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if strings.TrimSpace(apiKey) == "" {
		return Definition{}, nil, fmt.Errorf("no Anthropic API key (set it in settings or ANTHROPIC_API_KEY)")
	}
	if strings.TrimSpace(description) == "" {
		return Definition{}, nil, fmt.Errorf("describe the workflow you want")
	}

	raw, err := callClaude(ctx, apiKey, workflowGenPrompt(description))
	if err != nil {
		return Definition{}, nil, err
	}
	yamlDoc := extractYAML(raw)
	def, err := ParseDefinition([]byte(yamlDoc))
	if err != nil {
		// One self-repair attempt: hand the invalid YAML and the exact error back
		// to the model and ask it to fix it (models routinely fumble the single-root
		// rule on parallel-first flows).
		repaired, rerr := callClaude(ctx, apiKey, workflowRepairPrompt(yamlDoc, err))
		if rerr == nil {
			yamlDoc = extractYAML(repaired)
			def, err = ParseDefinition([]byte(yamlDoc))
		}
		if err != nil {
			return Definition{}, nil, fmt.Errorf("model produced an invalid workflow: %w\n---\n%s", err, yamlDoc)
		}
	}
	// Re-marshal so the saved file is clean and canonical.
	out, err := Marshal(def)
	if err != nil {
		return Definition{}, nil, err
	}
	return def, out, nil
}

func workflowGenPrompt(description string) string {
	return `You design multi-step coding workflows as a DAG. Convert the user's description into a workflow YAML.

Output ONLY the YAML (no prose, no markdown fences). Schema:

name: <kebab-case-id>
description: <one line>
steps:
  - name: <Step Name>
    executor: <claude|codex|gemini|pi|opencode|openclaw>   # optional, default claude
    model: <opus|sonnet|haiku>                             # optional, only meaningful for claude
    deps: [<earlier step names>]                           # omit for the first step
    prompt: |
      What this step should DO (imperative). Reference the goal as {{goal}}.

Rules:
- Exactly ONE step has no deps (the single root/entry step). If the flow starts with parallel work (e.g. "try 3 approaches at once"), add ONE root step first (e.g. a "Kickoff" or "Plan" step that frames {{goal}}) that all the parallel steps depend on.
- It is a DAG: deps must reference earlier steps; no cycles.
- Two steps with the same deps run in PARALLEL; a step depending on several steps JOINs them.
- Do NOT include any git/commit/push/PR/taskyou instructions in prompts — the system adds the branch handoff automatically. Prompts describe only the work.
- Keep prompts concise and concrete. Prefer strong models (opus) for planning/review, faster ones (sonnet/haiku) for mechanical steps.

User's description:
` + description
}

func workflowRepairPrompt(badYAML string, err error) string {
	return fmt.Sprintf(`This workflow YAML is invalid: %v

Fix it and output ONLY the corrected YAML (no prose, no fences). Keep the same schema and intent. Remember: exactly ONE step may have no deps — if several steps were parallel entry points, add a single root step they all depend on.

--- invalid yaml ---
%s`, err, badYAML)
}

// extractYAML pulls the YAML body out of a model response, tolerating markdown
// code fences the model may add despite instructions.
func extractYAML(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "yaml")
		s = strings.TrimPrefix(s, "yml")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}

// --- minimal Anthropic client (mirrors internal/ai) ---

type genMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type genRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []genMessage `json:"messages"`
}

type genContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type genResponse struct {
	Content []genContent `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func callClaude(ctx context.Context, apiKey, prompt string) (string, error) {
	body, err := json.Marshal(genRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 2000,
		Messages:  []genMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("anthropic API %d: %s", resp.StatusCode, string(b))
	}
	var out genResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic API: %s", out.Error.Message)
	}
	if len(out.Content) == 0 {
		return "", fmt.Errorf("empty response from model")
	}
	return out.Content[0].Text, nil
}
