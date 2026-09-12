package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/github"
)

// The detail view used to reserve a fixed six rows for a header whose height is
// variable. Every optional header line — a PR link, a stand, a host line, a pane
// notice — pushed the render one row past the pane, and the terminal scrolled the
// top away: the box border and the badge row carrying the status, PINNED, the
// project and the PR status went with it. The rendered view must fit the pane.
func TestDetailModel_ViewFitsPaneHeight(t *testing.T) {
	const paneHeight = 30

	newModel := func(mutate func(*DetailModel)) *DetailModel {
		m := &DetailModel{
			task: &db.Task{
				ID:      5356,
				Title:   "Catalog sync throttling",
				Status:  db.StatusProcessing,
				Project: "offerlab",
				Type:    "code",
				Body:    strings.Repeat("body line\n", 80),
			},
			focused: true,
			width:   180,
			height:  paneHeight,
		}
		if mutate != nil {
			mutate(m)
		}
		m.initViewport()
		return m
	}

	tests := []struct {
		name   string
		mutate func(*DetailModel)
	}{
		{"badges only", nil},
		{"pr link", func(m *DetailModel) {
			m.prInfo = &github.PRInfo{Number: 3627, URL: "https://github.com/offerlab/offerlab/pull/3627", State: github.PRStateOpen}
		}},
		{"pr link and stand", func(m *DetailModel) {
			m.prInfo = &github.PRInfo{Number: 3627, URL: "https://github.com/offerlab/offerlab/pull/3627", State: github.PRStateOpen}
			m.task.Summary = "Sidekiq retry vs rate limiting"
		}},
		{"placed with notice, error and stand", func(m *DetailModel) {
			m.prInfo = &github.PRInfo{Number: 3627, URL: "https://github.com/offerlab/offerlab/pull/3627", State: github.PRStateOpen}
			m.task.Summary = "Sidekiq retry vs rate limiting"
			m.task.PlacementTarget = "ol-agents"
			m.task.PlacementReason = "placed by hand"
			m.paneNotice = "The session on ol-agents is live"
			m.paneError = "spawn failed once"
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(tc.mutate)
			got := lipgloss.Height(m.View())
			if got > paneHeight {
				t.Errorf("view is %d rows in a %d-row pane; the top %d rows scroll off, taking the badge line with them",
					got, paneHeight, got-paneHeight)
			}
			if !strings.Contains(m.View(), "PINNED") && m.task.Pinned {
				t.Error("pinned badge missing")
			}
		})
	}
}

// A taller header must cost the viewport rows, not the pane.
func TestDetailModel_HeaderHeightTracksHeader(t *testing.T) {
	base := &DetailModel{
		task:    &db.Task{ID: 1, Title: "t", Status: db.StatusProcessing},
		focused: true,
		width:   180,
		height:  30,
	}
	base.initViewport()

	tall := &DetailModel{
		task:    &db.Task{ID: 1, Title: "t", Status: db.StatusProcessing, Summary: "a stand line"},
		focused: true,
		width:   180,
		height:  30,
	}
	tall.initViewport()

	if tall.headerHeight() != base.headerHeight()+1 {
		t.Errorf("stand line grew the header from %d to %d reserved rows; want one more",
			base.headerHeight(), tall.headerHeight())
	}
	if tall.viewport.Height != base.viewport.Height-1 {
		t.Errorf("viewport height %d did not give back the row the header took (base %d)",
			tall.viewport.Height, base.viewport.Height)
	}
}
