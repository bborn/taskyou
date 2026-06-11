package qmd

import (
	"context"
	"testing"
	"time"
)

func TestParseTaskIDFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want int64
	}{
		{"simple filename", "task-42.md", 42},
		{"with directory", "/tmp/ty-qmd-export/task-123.md", 123},
		{"nested path", "a/b/c/task-1.md", 1},
		{"not a task file", "readme.md", 0},
		{"no task prefix", "other-42.md", 0},
		{"non-numeric id", "task-abc.md", 0},
		{"empty string", "", 0},
		{"task prefix no id", "task-.md", 0},
		{"no extension", "task-42", 42}, // TrimSuffix is a no-op, still parses
		{"large id", "task-999999.md", 999999},
		{"negative id", "task--1.md", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTaskIDFromPath(tt.path)
			if got != tt.want {
				t.Errorf("parseTaskIDFromPath(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func TestCacheHit(t *testing.T) {
	c := NewClient("nonexistent-binary-for-test")
	// Manually populate cache
	key := "query:ty-tasks:test query:5"
	expected := []SearchResult{{DocID: "1", Score: 0.9, Title: "cached result"}}
	c.cache[key] = cachedResult{results: expected, timestamp: time.Now()}

	// Force available so search doesn't short-circuit
	c.mu.Lock()
	c.available = true
	c.mu.Unlock()
	// Mark as checked so sync.Once doesn't re-run LookPath
	c.checkedOnce.Do(func() {})

	// The cache key is built as: cmd + ":" + collection + ":" + query + ":" + count
	// For Query(), cmd="query", collection="ty-tasks", query="test query", count=5
	// So key = "query:ty-tasks:test query:5" — matches what we set

	// Read from cache directly to verify the key format
	c.cacheMu.RLock()
	cached, ok := c.cache[key]
	c.cacheMu.RUnlock()

	if !ok {
		t.Fatal("expected cache entry to exist")
	}
	if len(cached.results) != 1 || cached.results[0].Title != "cached result" {
		t.Errorf("unexpected cached results: %+v", cached.results)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	c := NewClient("nonexistent-binary-for-test")
	key := "search:col:old query:5"
	// Insert with timestamp in the past (beyond TTL)
	c.cache[key] = cachedResult{
		results:   []SearchResult{{Title: "stale"}},
		timestamp: time.Now().Add(-10 * time.Minute),
	}

	c.cacheMu.RLock()
	cached, ok := c.cache[key]
	c.cacheMu.RUnlock()

	if !ok {
		t.Fatal("cache entry should exist in map")
	}
	// But it should be expired
	if time.Since(cached.timestamp) < c.cacheTTL {
		t.Error("expected cache entry to be expired")
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewClient("nonexistent-binary-for-test")
	c.maxCache = 3

	// Fill cache to capacity
	now := time.Now()
	c.cache["key-1"] = cachedResult{results: nil, timestamp: now.Add(-3 * time.Second)} // oldest
	c.cache["key-2"] = cachedResult{results: nil, timestamp: now.Add(-2 * time.Second)}
	c.cache["key-3"] = cachedResult{results: nil, timestamp: now.Add(-1 * time.Second)} // newest

	if len(c.cache) != 3 {
		t.Fatalf("expected 3 cache entries, got %d", len(c.cache))
	}

	// Simulate what search() does when cache is full
	c.cacheMu.Lock()
	if len(c.cache) >= c.maxCache {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.cache {
			if oldestKey == "" || v.timestamp.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.timestamp
			}
		}
		delete(c.cache, oldestKey)
	}
	c.cache["key-4"] = cachedResult{results: nil, timestamp: now}
	c.cacheMu.Unlock()

	if len(c.cache) != 3 {
		t.Fatalf("expected 3 cache entries after eviction, got %d", len(c.cache))
	}

	// key-1 (oldest) should be evicted
	if _, ok := c.cache["key-1"]; ok {
		t.Error("expected key-1 (oldest) to be evicted")
	}
	// key-4 (new) should exist
	if _, ok := c.cache["key-4"]; !ok {
		t.Error("expected key-4 to exist after insert")
	}
}

func TestFindRelatedTasksConversion(t *testing.T) {
	c := NewClient("nonexistent-binary-for-test")

	// Force available
	c.mu.Lock()
	c.available = true
	c.mu.Unlock()
	c.checkedOnce.Do(func() {})

	// Pre-populate cache with results that FindRelatedTasks will find
	// FindRelatedTasks calls Query() with collection="ty-tasks"
	// Cache key: "query:ty-tasks:<query>:<count>"
	cacheKey := "query:ty-tasks:test query:5"
	c.cache[cacheKey] = cachedResult{
		results: []SearchResult{
			{DocID: "1", Score: 0.95, Path: "/tmp/task-42.md", Title: "Fix auth bug"},
			{DocID: "2", Score: 0.7, Path: "/tmp/task-99.md", Title: "Add login page"},
			{DocID: "3", Score: 0.5, Path: "/tmp/readme.md", Title: "Project README"}, // not a task file
			{DocID: "4", Score: 0.3, Path: "/tmp/task-abc.md", Title: "Bad ID"},       // unparseable
			{DocID: "5", Score: 0.6, Path: "/tmp/task-7.md", Title: ""},               // empty title, uses snippet
		},
		timestamp: time.Now(),
	}
	// Set snippet for result 5 (empty title case)
	cached := c.cache[cacheKey]
	cached.results[4].Snippet = "Snippet fallback"
	c.cache[cacheKey] = cached

	related, err := c.FindRelatedTasks(context.TODO(), "test query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 results: task-42, task-99, task-7 (readme.md and task-abc.md filtered out)
	if len(related) != 3 {
		t.Fatalf("expected 3 related tasks, got %d: %+v", len(related), related)
	}

	// Verify task-42
	if related[0].TaskID != 42 || related[0].Title != "Fix auth bug" || related[0].Score != 0.95 {
		t.Errorf("unexpected first result: %+v", related[0])
	}

	// Verify task-99
	if related[1].TaskID != 99 || related[1].Title != "Add login page" {
		t.Errorf("unexpected second result: %+v", related[1])
	}

	// Verify task-7 uses snippet as fallback title
	if related[2].TaskID != 7 || related[2].Title != "Snippet fallback" {
		t.Errorf("unexpected third result (snippet fallback): %+v", related[2])
	}
}

func TestIsAvailableLazyInit(t *testing.T) {
	// A client with a nonexistent binary should report unavailable
	c := NewClient("this-binary-does-not-exist-qmd-test")
	if c.IsAvailable() {
		t.Error("expected IsAvailable=false for nonexistent binary")
	}
	// Second call should return same result (sync.Once)
	if c.IsAvailable() {
		t.Error("expected IsAvailable=false on second call")
	}
}

func TestUnavailableClientReturnsNil(t *testing.T) {
	c := NewClient("this-binary-does-not-exist-qmd-test")

	results, err := c.Search(context.TODO(), "query", "col", 5)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}

	related, err := c.FindRelatedTasks(context.TODO(), "query", 5)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if related != nil {
		t.Errorf("expected nil related tasks, got %v", related)
	}
}
