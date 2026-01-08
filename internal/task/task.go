// Package task provides the core task model and utilities.
package task

import (
	"strings"
	"time"
)

// Status represents task status.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusBlocked    Status = "blocked"
	StatusNone       Status = ""
)

// Type represents task type.
type Type string

const (
	TypeCode     Type = "code"
	TypeWriting  Type = "writing"
	TypeThinking Type = "thinking"
	TypeNone     Type = ""
)

// Priority represents task priority.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = ""
	PriorityLow    Priority = "low"
)

// Project represents a project.
type Project string

const (
	ProjectOfferlab     Project = "offerlab"
	ProjectInfluenceKit Project = "influencekit"
	ProjectPersonal     Project = "personal"
	ProjectNone         Project = ""
)

// Task represents a task from the GitHub issue queue.
type Task struct {
	Number    int
	Title     string
	Body      string
	URL       string
	State     string
	Status    Status
	Type      Type
	Priority  Priority
	Project   Project
	CreatedAt time.Time
	UpdatedAt time.Time
	Labels    []string
	Comments  []Comment
}

// Comment represents a comment on a task.
type Comment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// ParseLabels extracts status, type, priority, and project from labels.
func (t *Task) ParseLabels() {
	for _, label := range t.Labels {
		switch {
		case strings.HasPrefix(label, "status:"):
			t.Status = Status(strings.TrimPrefix(label, "status:"))
		case strings.HasPrefix(label, "type:"):
			t.Type = Type(strings.TrimPrefix(label, "type:"))
		case strings.HasPrefix(label, "priority:"):
			t.Priority = Priority(strings.TrimPrefix(label, "priority:"))
		case strings.HasPrefix(label, "project:"):
			t.Project = Project(strings.TrimPrefix(label, "project:"))
		}
	}
}

// StatusIcon returns an icon for the task status.
func (t *Task) StatusIcon() string {
	switch t.Status {
	case StatusInProgress:
		return "⋯"
	case StatusDone:
		return "✓"
	case StatusBlocked:
		return "!"
	default:
		return "·"
	}
}

// ProjectShort returns a short project name.
func (t *Task) ProjectShort() string {
	switch t.Project {
	case ProjectOfferlab:
		return "ol"
	case ProjectInfluenceKit:
		return "ik"
	case ProjectPersonal:
		return "personal"
	default:
		return ""
	}
}

// TypeShort returns a short type name.
func (t *Task) TypeShort() string {
	switch t.Type {
	case TypeCode:
		return "code"
	case TypeWriting:
		return "write"
	case TypeThinking:
		return "think"
	default:
		return ""
	}
}

// IsHighPriority returns true if the task is high priority.
func (t *Task) IsHighPriority() bool {
	return t.Priority == PriorityHigh
}

// FilterOptions represents options for filtering tasks.
type FilterOptions struct {
	Project  Project
	Type     Type
	Status   Status
	Priority Priority
	State    string // "open", "closed", "all"
	Limit    int
}

// DefaultFilterOptions returns sensible defaults.
func DefaultFilterOptions() FilterOptions {
	return FilterOptions{
		State: "open",
		Limit: 30,
	}
}

// NormalizeProject converts short names to full project names.
func NormalizeProject(s string) Project {
	switch strings.ToLower(s) {
	case "offerlab", "ol", "o":
		return ProjectOfferlab
	case "influencekit", "ik", "i":
		return ProjectInfluenceKit
	case "personal", "p":
		return ProjectPersonal
	default:
		return Project(s)
	}
}

// NormalizeType converts short names to full type names.
func NormalizeType(s string) Type {
	switch strings.ToLower(s) {
	case "code", "c":
		return TypeCode
	case "writing", "write", "w":
		return TypeWriting
	case "thinking", "think", "t":
		return TypeThinking
	default:
		return Type(s)
	}
}

// NormalizeStatus converts short names to full status names.
func NormalizeStatus(s string) Status {
	switch strings.ToLower(s) {
	case "backlog", "back", "b":
		return StatusBacklog
	case "in_progress", "inprogress", "progress", "ip", "p":
		return StatusInProgress
	case "done", "d", "ready", "r":
		return StatusDone
	case "blocked", "block", "x":
		return StatusBlocked
	default:
		return Status(s)
	}
}

// NormalizePriority converts short names to full priority names.
func NormalizePriority(s string) Priority {
	switch strings.ToLower(s) {
	case "high", "h", "1":
		return PriorityHigh
	case "low", "l", "3":
		return PriorityLow
	default:
		return PriorityNormal
	}
}
