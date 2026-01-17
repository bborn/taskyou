// Package autocomplete provides LLM-powered ghost text suggestions for task input.
package autocomplete

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// cacheEntry represents a cached autocomplete suggestion.
type cacheEntry struct {
	suggestion string
	timestamp  time.Time
}

// Service handles autocomplete requests with debouncing and cancellation.
type Service struct {
	mu            sync.Mutex
	nextRequestID int64
	apiKey        string
	httpClient    *http.Client

	// LRU cache for suggestions (key: "fieldType:project:input")
	cache      map[string]*cacheEntry
	cacheOrder []string // For LRU eviction
	cacheMu    sync.RWMutex
	cacheSize  int
	cacheTTL   time.Duration
}

// NewService creates a new autocomplete service.
// apiKey can be empty - it will check ANTHROPIC_API_KEY env var.
func NewService(apiKey string) *Service {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	// Create HTTP client with connection pooling for faster subsequent requests
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}

	s := &Service{
		apiKey: apiKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second, // Overall HTTP timeout
		},
		cache:     make(map[string]*cacheEntry),
		cacheSize: 200,              // Increased cache for better hit rates
		cacheTTL:  10 * time.Minute, // Longer TTL for session work
	}

	if apiKey != "" {
		s.log("Service initialized with API key: %s...%s", apiKey[:7], apiKey[len(apiKey)-4:])
	} else {
		s.log("Service initialized WITHOUT API key")
	}

	return s
}

// IsAvailable returns true if the autocomplete service has an API key configured.
func (s *Service) IsAvailable() bool {
	return s.apiKey != ""
}

// Warmup pre-warms the HTTP connection by making a lightweight API request.
// This establishes the TLS connection so subsequent requests are faster.
func (s *Service) Warmup() {
	if s.apiKey == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Make a lightweight request to establish TLS connection
		req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
		if err != nil {
			return
		}
		req.Header.Set("x-api-key", s.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		s.log("Warmup complete")
	}()
}

// Cancel is a no-op - cancellation is handled via context.
func (s *Service) Cancel() {}

// normalizeInput normalizes input for better cache hit rates.
// Trims trailing whitespace and converts to lowercase for key matching.
func normalizeInput(input string) string {
	return strings.ToLower(strings.TrimRight(input, " \t"))
}

// getFromCache retrieves a cached suggestion if available and not expired.
// Supports prefix matching - if we have a cached suggestion for "Fix the bug in auth",
// it can match input "Fix the" if the suggestion starts with the input.
func (s *Service) getFromCache(key, input, fieldType string) string {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	// Direct match first (most common case)
	entry, ok := s.cache[key]
	if ok && time.Since(entry.timestamp) <= s.cacheTTL {
		return entry.suggestion
	}

	// For title field, try prefix matching - find cached entries that could complete this input
	if fieldType == "title" {
		normalizedInput := normalizeInput(input)
		for _, cachedKey := range s.cacheOrder {
			entry := s.cache[cachedKey]
			if entry == nil || time.Since(entry.timestamp) > s.cacheTTL {
				continue
			}

			// Check if cached suggestion starts with current input (case-insensitive)
			normalizedSuggestion := strings.ToLower(entry.suggestion)
			if strings.HasPrefix(normalizedSuggestion, normalizedInput) {
				s.log("Cache prefix match: %q -> %q", input, entry.suggestion)
				return entry.suggestion
			}
		}
	}

	return ""
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
// The context should be cancelled if the user continues typing.
// recentTasks provides context about recent task titles for better suggestions.
func (s *Service) GetSuggestion(ctx context.Context, input, fieldType, project, extraContext string, recentTasks []string) *Suggestion {
	s.log("GetSuggestion called: field=%s input=%q", fieldType, input)

	if s.apiKey == "" {
		s.log("No API key, returning nil")
		return nil
	}

	// Don't suggest for very short inputs (except body_suggest which uses title as context)
	if fieldType != "body_suggest" {
		minLen := 3
		if fieldType == "title" {
			minLen = 2
		}
		if len(strings.TrimSpace(input)) < minLen {
			s.log("Input too short (%d < %d), returning nil", len(strings.TrimSpace(input)), minLen)
			return nil
		}
	}

	// Check cache first - instant response for repeated inputs
	// Include extraContext in cache key for body_suggest (title-based suggestions)
	// Use normalized input for better cache hit rates
	normalizedInput := normalizeInput(input)
	cacheKey := fmt.Sprintf("%s:%s:%s:%s", fieldType, project, normalizedInput, extraContext)
	if cached := s.getFromCache(cacheKey, input, fieldType); cached != "" {
		s.log("Cache hit for: %s", cacheKey)
		return s.processSuggestion(cached, input, fieldType, 0)
	}

	// Track request ID
	s.mu.Lock()
	s.nextRequestID++
	reqID := s.nextRequestID
	s.mu.Unlock()

	// Build prompt and call API
	prompt := buildPrompt(input, fieldType, project, extraContext, recentTasks)
	suggestion, err := s.callAPI(ctx, prompt)
	if err != nil {
		return nil
	}

	// Cache the result for future use
	s.addToCache(cacheKey, suggestion)

	return s.processSuggestion(suggestion, input, fieldType, reqID)
}

// processSuggestion validates and transforms a raw suggestion into a Suggestion struct.
func (s *Service) processSuggestion(suggestion, input, fieldType string, reqID int64) *Suggestion {
	// Validate the suggestion
	suggestion = strings.TrimSpace(suggestion)
	if suggestion == "" {
		return nil
	}

	// Remove any quotes the LLM might have added
	suggestion = strings.Trim(suggestion, "\"'")

	// For body field, the suggestion IS the continuation (not full text)
	if fieldType == "body" || fieldType == "body_suggest" {
		// The API returns only what comes next, use it directly as suffix
		return &Suggestion{
			Text:      suggestion,
			FullText:  input + suggestion,
			RequestID: reqID,
		}
	}

	// For title field: LLM returns full completion, extract the suffix
	// Check if suggestion equals input (no completion)
	if strings.EqualFold(suggestion, input) {
		return nil
	}

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

func buildPrompt(input, fieldType, project, extraContext string, recentTasks []string) string {
	var sb strings.Builder

	if fieldType == "title" {
		sb.WriteString("You are an autocomplete system. Complete the partial task title with a natural ending. Output ONLY the completed title text, no explanations.\n")
		if project != "" && project != "personal" {
			sb.WriteString(fmt.Sprintf("Project: %s\n", project))
		}
		if len(recentTasks) > 0 {
			sb.WriteString("Recent tasks for style:\n")
			for _, t := range recentTasks {
				sb.WriteString(fmt.Sprintf("- %s\n", t))
			}
		}
		sb.WriteString(fmt.Sprintf("Partial title: %s", input))
	} else if fieldType == "body_suggest" {
		// Suggest a description based on the task title (extraContext contains the title)
		sb.WriteString("You are an autocomplete system. Suggest a brief 1-sentence task description. Output ONLY the description text, no explanations.\n")
		sb.WriteString(fmt.Sprintf("Task title: %s\nDescription:", extraContext))
	} else {
		sb.WriteString("You are an autocomplete system. Continue this text briefly (1 sentence max). Output ONLY the continuation text, no explanations.\n")
		if extraContext != "" {
			sb.WriteString(fmt.Sprintf("Task: %s\n", extraContext))
		}
		sb.WriteString(fmt.Sprintf("Text so far: %s\nContinuation:", input))
	}

	return sb.String()
}

// Anthropic API types
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

func (s *Service) log(format string, args ...interface{}) {
	f, err := os.OpenFile("/tmp/autocomplete.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05"), msg)
}

func (s *Service) callAPI(ctx context.Context, prompt string) (string, error) {
	s.log("REQUEST: %s", prompt[:min(80, len(prompt))])

	reqBody := anthropicRequest{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 50,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		s.log("ERROR marshal: %v", err)
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		s.log("ERROR request: %v", err)
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log("ERROR http: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.log("ERROR status: %d body: %s", resp.StatusCode, string(body))
		return "", fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var apiResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		s.log("ERROR decode: %v", err)
		return "", err
	}

	if apiResp.Error != nil {
		s.log("ERROR api: %s", apiResp.Error.Message)
		return "", fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		s.log("ERROR empty response")
		return "", fmt.Errorf("empty response")
	}

	result := apiResp.Content[0].Text
	s.log("RESPONSE (%dms): %s", time.Since(start).Milliseconds(), result[:min(80, len(result))])
	return result, nil
}
