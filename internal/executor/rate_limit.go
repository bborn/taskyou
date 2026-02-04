// Package executor provides task execution with pluggable executor backends.
package executor

import (
	"regexp"
	"strings"
)

// RateLimitPattern defines a pattern that indicates a rate limit error.
type RateLimitPattern struct {
	Executor string         // Which executor this pattern applies to ("*" for all)
	Pattern  *regexp.Regexp // Regex pattern to match
	Message  string         // Human-readable description of the rate limit
}

// rateLimitPatterns contains all known rate limit error patterns.
// These patterns are matched against executor output to detect when rate limits are hit.
var rateLimitPatterns = []RateLimitPattern{
	// Claude/Anthropic patterns
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)rate limit|rate_limit|rate-limit`), Message: "Anthropic rate limit hit"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)429|too many requests`), Message: "HTTP 429 Too Many Requests"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)resource exhausted|ResourceExhausted`), Message: "Resource exhausted"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)overloaded|capacity|busy`), Message: "Service overloaded"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)quota exceeded|quota_exceeded`), Message: "Quota exceeded"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)context.*limit|token.*limit|max.*token`), Message: "Context/token limit exceeded"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)usage limit|usage_limit`), Message: "Usage limit reached"},

	// OpenAI/Codex patterns
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)rate limit|rate_limit|rate-limit`), Message: "OpenAI rate limit hit"},
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)429|too many requests`), Message: "HTTP 429 Too Many Requests"},
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)tokens per min|TPM|RPM`), Message: "Token/request per minute limit"},
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)insufficient_quota`), Message: "Insufficient quota"},
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)billing.*limit|spending.*limit`), Message: "Billing limit reached"},

	// Google/Gemini patterns
	{Executor: "gemini", Pattern: regexp.MustCompile(`(?i)rate limit|rate_limit|rate-limit`), Message: "Google rate limit hit"},
	{Executor: "gemini", Pattern: regexp.MustCompile(`(?i)429|too many requests|RESOURCE_EXHAUSTED`), Message: "HTTP 429 or resource exhausted"},
	{Executor: "gemini", Pattern: regexp.MustCompile(`(?i)quota exceeded|quotaExceeded`), Message: "Quota exceeded"},

	// Generic patterns that apply to all executors
	{Executor: "*", Pattern: regexp.MustCompile(`(?i)please try again later`), Message: "Temporary error - retry later"},
	{Executor: "*", Pattern: regexp.MustCompile(`(?i)temporarily unavailable`), Message: "Service temporarily unavailable"},
	{Executor: "*", Pattern: regexp.MustCompile(`(?i)service.*overloaded`), Message: "Service overloaded"},
}

// DetectRateLimit checks if the output contains rate limit error patterns.
// Returns true if a rate limit is detected, along with the matched pattern's message.
func DetectRateLimit(output string, executorName string) (bool, string) {
	for _, pattern := range rateLimitPatterns {
		// Check if pattern applies to this executor or all executors
		if pattern.Executor != "*" && pattern.Executor != executorName {
			continue
		}

		if pattern.Pattern.MatchString(output) {
			return true, pattern.Message
		}
	}
	return false, ""
}

// DetectRateLimitInLines checks multiple lines of output for rate limit patterns.
// This is useful for checking recent tmux output.
func DetectRateLimitInLines(lines []string, executorName string) (bool, string) {
	// Join lines and check for patterns
	combined := strings.Join(lines, "\n")
	return DetectRateLimit(combined, executorName)
}

// ContextLimitPattern defines patterns for context length limits.
var contextLimitPatterns = []RateLimitPattern{
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)context.*too long|exceeds.*context|context.*window`), Message: "Context length exceeded"},
	{Executor: "claude", Pattern: regexp.MustCompile(`(?i)maximum.*tokens|token limit|max tokens`), Message: "Maximum tokens exceeded"},
	{Executor: "codex", Pattern: regexp.MustCompile(`(?i)context_length_exceeded|max_tokens`), Message: "Context length exceeded"},
	{Executor: "gemini", Pattern: regexp.MustCompile(`(?i)CONTEXT_LENGTH_EXCEEDED`), Message: "Context length exceeded"},
}

// DetectContextLimit checks if the output contains context length limit errors.
// Returns true if a context limit is detected, along with the matched pattern's message.
func DetectContextLimit(output string, executorName string) (bool, string) {
	for _, pattern := range contextLimitPatterns {
		if pattern.Executor != "*" && pattern.Executor != executorName {
			continue
		}

		if pattern.Pattern.MatchString(output) {
			return true, pattern.Message
		}
	}
	return false, ""
}
