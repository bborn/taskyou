// Package autocomplete provides LLM-powered ghost text suggestions for task input.
package autocomplete

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Suggestion represents a completion suggestion from the LLM.
type Suggestion struct {
	Text      string // The suggested completion text (suffix to append)
	FullText  string // The full text including user input + suggestion
	RequestID int64  // To match responses with requests
}

// Request represents an autocomplete request.
type Request struct {
	ID         int64
	Input      string // Current user input
	FieldType  string // "title" or "body"
	Project    string // Project context
	Context    string // Additional context (e.g., title when completing body)
	CancelFunc context.CancelFunc
}

// cacheEntry represents a cached autocomplete suggestion.
type cacheEntry struct {
	suggestion string
	timestamp  time.Time
}

// Service handles autocomplete requests with debouncing and cancellation.
type Service struct {
	mu            sync.Mutex
	currentReq    *Request
	nextRequestID int64
	debounceDelay time.Duration
	timeout       time.Duration

	// LRU cache for suggestions (key: "fieldType:project:input")
	cache      map[string]*cacheEntry
	cacheOrder []string // For LRU eviction
	cacheMu    sync.RWMutex
	cacheSize  int
	cacheTTL   time.Duration

	// Warmup state
	warmedUp bool
}

// NewService creates a new autocomplete service.
func NewService() *Service {
	return &Service{
		debounceDelay: 100 * time.Millisecond, // Fast debounce for responsiveness
		timeout:       2 * time.Second,        // Quick timeout - haiku is fast
		cache:         make(map[string]*cacheEntry),
		cacheSize:     100,             // Keep last 100 suggestions
		cacheTTL:      5 * time.Minute, // Cache entries valid for 5 minutes
	}
}

// Warmup pre-warms the Claude CLI by running a quick request in the background.
// This helps reduce latency for the first real autocomplete request.
func (s *Service) Warmup() {
	s.mu.Lock()
	if s.warmedUp {
		s.mu.Unlock()
		return
	}
	s.warmedUp = true
	s.mu.Unlock()

	// Run warmup in background - don't block
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Simple warmup prompt - just get Claude CLI loaded and ready
		args := []string{
			"-p",
			"--model", "haiku",
			"--output-format", "json",
			"hi",
		}
		cmd := exec.CommandContext(ctx, "claude", args...)
		cmd.Dir = "/tmp"
		_ = cmd.Run() // Ignore result - just warming up
	}()
}

// Cancel cancels any pending autocomplete request.
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentReq != nil && s.currentReq.CancelFunc != nil {
		s.currentReq.CancelFunc()
		s.currentReq = nil
	}
}

// getFromCache retrieves a cached suggestion if available and not expired.
func (s *Service) getFromCache(key string) string {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	entry, ok := s.cache[key]
	if !ok {
		return ""
	}

	// Check if entry has expired
	if time.Since(entry.timestamp) > s.cacheTTL {
		return ""
	}

	return entry.suggestion
}

// addToCache adds a suggestion to the cache with LRU eviction.
func (s *Service) addToCache(key, suggestion string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Check if key already exists (update timestamp)
	if _, exists := s.cache[key]; exists {
		s.cache[key] = &cacheEntry{
			suggestion: suggestion,
			timestamp:  time.Now(),
		}
		// Move to end of order (most recently used)
		for i, k := range s.cacheOrder {
			if k == key {
				s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
				s.cacheOrder = append(s.cacheOrder, key)
				break
			}
		}
		return
	}

	// Evict oldest entry if cache is full
	if len(s.cache) >= s.cacheSize && len(s.cacheOrder) > 0 {
		oldest := s.cacheOrder[0]
		s.cacheOrder = s.cacheOrder[1:]
		delete(s.cache, oldest)
	}

	// Add new entry
	s.cache[key] = &cacheEntry{
		suggestion: suggestion,
		timestamp:  time.Now(),
	}
	s.cacheOrder = append(s.cacheOrder, key)
}

// GetSuggestion gets an autocomplete suggestion for the given input.
// This is a blocking call that should be run in a goroutine.
// Returns nil if cancelled, timed out, or no good suggestion.
func (s *Service) GetSuggestion(ctx context.Context, input, fieldType, project, extraContext string) *Suggestion {
	// Don't suggest for very short inputs
	minLen := 3
	if fieldType == "title" {
		minLen = 2
	}
	if len(strings.TrimSpace(input)) < minLen {
		return nil
	}

	// Check cache first - instant response for repeated inputs
	cacheKey := fmt.Sprintf("%s:%s:%s", fieldType, project, input)
	if cached := s.getFromCache(cacheKey); cached != "" {
		return s.processSuggestion(cached, input, 0)
	}

	// Create cancellable context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Register this request
	s.mu.Lock()
	// Cancel any existing request
	if s.currentReq != nil && s.currentReq.CancelFunc != nil {
		s.currentReq.CancelFunc()
	}
	s.nextRequestID++
	reqID := s.nextRequestID
	s.currentReq = &Request{
		ID:         reqID,
		Input:      input,
		FieldType:  fieldType,
		Project:    project,
		CancelFunc: cancel,
	}
	s.mu.Unlock()

	// Build prompt based on field type
	prompt := buildPrompt(input, fieldType, project, extraContext)

	// Call Claude CLI with Haiku for speed
	suggestion, err := callClaude(ctx, prompt)
	if err != nil {
		return nil
	}

	// Cache the result for future use
	s.addToCache(cacheKey, suggestion)

	return s.processSuggestion(suggestion, input, reqID)
}

// processSuggestion validates and transforms a raw suggestion into a Suggestion struct.
func (s *Service) processSuggestion(suggestion, input string, reqID int64) *Suggestion {
	// Validate the suggestion
	suggestion = strings.TrimSpace(suggestion)
	if suggestion == "" {
		return nil
	}

	// Remove any quotes the LLM might have added
	suggestion = strings.Trim(suggestion, "\"'")

	// Check if suggestion equals input (no completion)
	if strings.EqualFold(suggestion, input) {
		return nil
	}

	// The LLM should return the full completion, extract the suffix
	// Handle case-insensitive prefix matching
	if !strings.HasPrefix(strings.ToLower(suggestion), strings.ToLower(input)) {
		// If it doesn't start with our input, it's not a valid continuation
		return nil
	}

	// Use the original input's case for the prefix, append the new suffix
	suffix := suggestion[len(input):]
	if suffix == "" {
		return nil
	}

	// Construct full text preserving user's original input casing
	fullText := input + suffix

	return &Suggestion{
		Text:      suffix,
		FullText:  fullText,
		RequestID: reqID,
	}
}

func buildPrompt(input, fieldType, project, extraContext string) string {
	var sb strings.Builder

	// Very focused prompt for quick completions - designed to get a single short completion
	if fieldType == "title" {
		sb.WriteString("You are an autocomplete assistant. Complete this partial task title with a natural ending.\n")
		sb.WriteString("Rules: Output ONLY the completed title. No explanations. No questions. Just the title.\n\n")
		if project != "" && project != "personal" {
			sb.WriteString(fmt.Sprintf("Project context: %s\n", project))
		}
		sb.WriteString(fmt.Sprintf("Partial title: \"%s\"\n", input))
		sb.WriteString("Completed title:")
	} else {
		sb.WriteString("You are an autocomplete assistant. Complete this partial task description with a natural ending.\n")
		sb.WriteString("Rules: Output ONLY the completed description. No explanations. No questions. Keep it brief.\n\n")
		if extraContext != "" {
			sb.WriteString(fmt.Sprintf("Task title: %s\n", extraContext))
		}
		sb.WriteString(fmt.Sprintf("Partial description: \"%s\"\n", input))
		sb.WriteString("Completed description:")
	}

	return sb.String()
}

func callClaude(ctx context.Context, prompt string) (string, error) {
	// Use haiku for speed, run from /tmp to avoid project context loading
	args := []string{
		"-p",
		"--model", "haiku",
		"--output-format", "json",
		prompt,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = "/tmp" // Run from neutral directory to avoid loading project context
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude execution: %w", err)
	}

	// Parse JSON response - Claude CLI returns {"result": "...", "is_error": bool, ...}
	var response struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		// Try to extract text directly if not JSON
		return strings.TrimSpace(string(output)), nil
	}

	if response.IsError {
		return "", fmt.Errorf("claude returned error")
	}

	// Clean up the result - remove any extra explanation
	result := strings.TrimSpace(response.Result)

	// If the response contains multiple lines, take just the first meaningful one
	// (in case the model adds explanations)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(strings.ToLower(line), "here") &&
			!strings.HasPrefix(strings.ToLower(line), "the completed") {
			return line, nil
		}
	}

	return result, nil
}
