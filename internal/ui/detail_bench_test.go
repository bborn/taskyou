package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// benchTaskBody is a realistic, markdown-heavy task body — the kind of content
// that flows through glamour on the detail-view open path. Headings, lists, code
// fences and inline emphasis make glamour do real work.
func benchTaskBody() string {
	var b strings.Builder
	b.WriteString("# Implement the executor pane\n\n")
	b.WriteString("We need to make the executor pane **fast**, _fluid_, and instant-feeling.\n\n")
	b.WriteString("## Steps\n\n")
	for i := 1; i <= 8; i++ {
		b.WriteString(fmt.Sprintf("%d. Do the thing number %d with `some code` and a [link](https://example.com)\n", i, i))
	}
	b.WriteString("\n## Details\n\n")
	b.WriteString("```go\nfunc main() {\n\tfmt.Println(\"hello from the task body\")\n}\n```\n\n")
	for i := 0; i < 6; i++ {
		b.WriteString("- A reasonably long bullet point describing one more consideration we must keep in mind while implementing this.\n")
	}
	return b.String()
}

// benchDetail builds a DetailModel with a realistic markdown body + activity
// summary, sized for a wide terminal, ready to render. database is nil so the
// benchmarks isolate the render/markdown cost (the dependencies section is
// skipped when database is nil).
func benchDetail() *DetailModel {
	task := &db.Task{
		ID:      1,
		Title:   "Perf: executor pane slow to load",
		Status:  db.StatusProcessing,
		Project: "taskyou",
		Type:    "perf",
		Body:    benchTaskBody(),
		Summary: "The agent has been profiling the open path and found the synchronous tmux join blocking the UI thread. Work is ongoing.",
	}
	m := &DetailModel{
		task:    task,
		width:   160,
		height:  50,
		focused: true,
		ready:   true,
	}
	m.viewport.Width = m.width - 4
	m.viewport.Height = m.height - 8
	return m
}

// BenchmarkDetailRenderContentCached measures a re-render where nothing changed —
// the common case while a task sits open (focus ticks, unrelated events). The
// internal content cache should make this a few cheap string comparisons.
func BenchmarkDetailRenderContentCached(b *testing.B) {
	m := benchDetail()
	_ = m.renderContent() // warm the cache
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		sink = m.renderContent()
	}
	_ = sink
}

// BenchmarkDetailRenderContentCold measures rendering the task body + summary
// through glamour with the content cache cleared but the glamour renderer warm —
// the per-change cost (e.g. a new log line) once the view is open.
func BenchmarkDetailRenderContentCold(b *testing.B) {
	m := benchDetail()
	_ = m.renderContent() // build + cache the glamour renderer
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		m.cachedContent = "" // force a re-render, keep the cached renderer
		sink = m.renderContent()
	}
	_ = sink
}

// BenchmarkDetailRenderContentColdRenderer measures the worst case on a fresh
// open: a brand-new DetailModel re-builds the glamour renderer (chroma syntax
// highlighter, style parsing) before rendering. This is the per-open markdown
// cost that runs on the detail-view open path.
func BenchmarkDetailRenderContentColdRenderer(b *testing.B) {
	m := benchDetail()
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		m.cachedContent = ""
		m.glamourRendererFocused = nil
		m.glamourRendererUnfocused = nil
		m.glamourWidth = 0
		sink = m.renderContent()
	}
	_ = sink
}

// BenchmarkDetailRenderHeader measures the per-frame header render (badges, PR
// info, spinner) — rebuilt on every View() call.
func BenchmarkDetailRenderHeader(b *testing.B) {
	m := benchDetail()
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		sink = m.renderHeader()
	}
	_ = sink
}

// BenchmarkDetailViewIdle measures a re-render where nothing changed — the common
// case while a task sits open (focus ticks, polls, unrelated events). The View
// render cache should skip the expensive viewport + bordered box render, leaving
// only the cheap header/help render used to detect changes.
func BenchmarkDetailViewIdle(b *testing.B) {
	m := benchDetail()
	m.setViewportContent()
	_ = m.View() // warm the cache
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		sink = m.View()
	}
	_ = sink
}

// BenchmarkDetailViewCold measures a full detail View() with the render cache
// cleared — header + viewport + bordered box. This is the per-change cost (scroll,
// content update, focus change, resize) and the first paint.
func BenchmarkDetailViewCold(b *testing.B) {
	m := benchDetail()
	m.setViewportContent()
	b.ReportAllocs()
	b.ResetTimer()
	var sink string
	for i := 0; i < b.N; i++ {
		m.cachedViewOK = false
		sink = m.View()
	}
	_ = sink
}
