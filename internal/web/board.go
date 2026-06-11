// Package web provides a lightweight HTTP server for the TaskYou kanban board.
package web

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// BoardSnapshot is the top-level board state returned by the API.
type BoardSnapshot struct {
	Columns []BoardColumn `json:"columns"`
}

// BoardColumn is a single kanban column.
type BoardColumn struct {
	Status string       `json:"status"`
	Label  string       `json:"label"`
	Count  int          `json:"count"`
	Tasks  []BoardEntry `json:"tasks"`
}

// BoardEntry is a single task card in the board.
type BoardEntry struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Project string `json:"project"`
	Type    string `json:"type"`
	Pinned  bool   `json:"pinned"`
	AgeHint string `json:"age_hint"`
}

// BuildBoardSnapshot groups tasks into kanban columns.
func BuildBoardSnapshot(tasks []*db.Task, limit int) BoardSnapshot {
	sections := []struct {
		status string
		label  string
	}{
		{db.StatusBacklog, "Backlog"},
		{db.StatusProcessing, "In Progress"},
		{db.StatusBlocked, "Blocked"},
		{db.StatusDone, "Done"},
	}

	grouped := make(map[string][]*db.Task)
	for _, task := range tasks {
		if task.Status == db.StatusArchived {
			continue
		}
		status := task.Status
		// Fold queued into backlog for the board view
		if status == db.StatusQueued {
			status = db.StatusBacklog
		}
		grouped[status] = append(grouped[status], task)
	}

	var snapshot BoardSnapshot
	for _, section := range sections {
		columnTasks := grouped[section.status]
		if len(columnTasks) == 0 {
			snapshot.Columns = append(snapshot.Columns, BoardColumn{Status: section.status, Label: section.label, Count: 0})
			continue
		}

		sortTasksForBoard(columnTasks)
		column := BoardColumn{Status: section.status, Label: section.label, Count: len(columnTasks)}
		for i, task := range columnTasks {
			if i >= limit {
				break
			}
			entry := BoardEntry{
				ID:      task.ID,
				Title:   truncateTitle(task.Title, 80),
				Project: task.Project,
				Type:    task.Type,
				Pinned:  task.Pinned,
				AgeHint: boardAgeHint(task),
			}
			column.Tasks = append(column.Tasks, entry)
		}
		snapshot.Columns = append(snapshot.Columns, column)
	}

	return snapshot
}

func sortTasksForBoard(tasks []*db.Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Pinned != tasks[j].Pinned {
			return tasks[i].Pinned
		}
		return boardReferenceTime(tasks[i]).After(boardReferenceTime(tasks[j]))
	})
}

func boardReferenceTime(task *db.Task) time.Time {
	switch task.Status {
	case db.StatusProcessing:
		if task.StartedAt != nil {
			return task.StartedAt.Time
		}
	case db.StatusDone:
		if task.CompletedAt != nil {
			return task.CompletedAt.Time
		}
	case db.StatusBlocked:
		return task.UpdatedAt.Time
	case db.StatusQueued:
		return task.UpdatedAt.Time
	case db.StatusBacklog:
		return task.CreatedAt.Time
	}
	return task.UpdatedAt.Time
}

func boardAgeHint(task *db.Task) string {
	ref := boardReferenceTime(task)
	if ref.IsZero() {
		return ""
	}

	delta := time.Since(ref)
	if delta < 0 {
		delta = -delta
	}

	switch task.Status {
	case db.StatusProcessing:
		return fmt.Sprintf("running %s ago", formatShortDuration(delta))
	case db.StatusBlocked:
		return fmt.Sprintf("blocked %s", formatShortDuration(delta))
	case db.StatusQueued:
		return fmt.Sprintf("queued %s", formatShortDuration(delta))
	case db.StatusBacklog:
		return fmt.Sprintf("created %s", formatShortDuration(delta))
	case db.StatusDone:
		return fmt.Sprintf("done %s", formatShortDuration(delta))
	default:
		return formatShortDuration(delta)
	}
}

func formatShortDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	units := []struct {
		dur   time.Duration
		label string
	}{
		{24 * time.Hour, "d"},
		{time.Hour, "h"},
		{time.Minute, "m"},
		{time.Second, "s"},
	}
	var parts []string
	remainder := d
	for _, u := range units {
		if remainder >= u.dur {
			value := remainder / u.dur
			parts = append(parts, fmt.Sprintf("%d%s", value, u.label))
			remainder %= u.dur
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, "")
}

func truncateTitle(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
