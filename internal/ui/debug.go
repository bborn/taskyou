package ui

import (
	"encoding/json"
	"os"
	"time"
)

// DebugState represents the application state as a "Text DOM" for AI agents.
type DebugState struct {
	View         string             `json:"view"`
	Size         DebugSize          `json:"size"`
	Dashboard    *DebugDashboard    `json:"dashboard,omitempty"`
	Detail       *DebugDetail       `json:"detail,omitempty"`
	Form         *DebugForm         `json:"form,omitempty"`
	Modals       *DebugModals       `json:"modals,omitempty"`
	Filter       *DebugFilter       `json:"filter,omitempty"`
	Notification *DebugNotification `json:"notification,omitempty"`
	Timestamp    time.Time          `json:"timestamp"`
}

type DebugSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type DebugDashboard struct {
	Columns       []DebugColumn `json:"columns"`
	FocusedColumn int           `json:"focused_column"`
	SelectedTask  int64         `json:"selected_task_id,omitempty"`
}

type DebugColumn struct {
	Name   string      `json:"name"`
	Tasks  []DebugTask `json:"tasks"`
	Focus  bool        `json:"is_focused"`
	Status string      `json:"status"`
}

type DebugTask struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Project  string `json:"project"`
	Selected bool   `json:"is_selected"`
	Pinned   bool   `json:"is_pinned,omitempty"`
}

type DebugDetail struct {
	TaskID   int64  `json:"task_id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Project  string `json:"project"`
	Focused  bool   `json:"is_focused"`
	Logs     int    `json:"log_count"`
	HasPanes bool   `json:"has_panes"`
}

type DebugForm struct {
	Title        string            `json:"title"`
	Fields       map[string]string `json:"fields"`
	FocusedField string            `json:"focused_field"`
	Project      string            `json:"project"`
	Type         string            `json:"type"`
	Executor     string            `json:"executor"`
}

type DebugModals struct {
	ActiveConfirm string `json:"active_confirm,omitempty"` // "delete", "close", "archive", "quit", etc.
}

type DebugFilter struct {
	Active bool   `json:"active"`
	Text   string `json:"text"`
}

type DebugNotification struct {
	Message string `json:"message"`
	TaskID  int64  `json:"task_id,omitempty"`
}

// GenerateDebugState creates a snapshot of the current application state.
func (m *AppModel) GenerateDebugState() DebugState {
	s := DebugState{
		View:      viewName(m.currentView),
		Size:      DebugSize{Width: m.width, Height: m.height},
		Timestamp: time.Now(),
	}

	// Dashboard State
	if m.currentView == ViewDashboard {
		dash := &DebugDashboard{
			FocusedColumn: m.kanban.selectedCol,
		}

		// Reconstruct columns from kanban state
		for i, col := range m.kanban.columns {
			dCol := DebugColumn{
				Name:   col.Title,
				Status: col.Status,
				Focus:  i == m.kanban.selectedCol,
			}

			for j, t := range col.Tasks {
				dCol.Tasks = append(dCol.Tasks, DebugTask{
					ID:       t.ID,
					Title:    t.Title,
					Status:   t.Status,
					Project:  t.Project,
					Selected: i == m.kanban.selectedCol && j == m.kanban.selectedRow,
					Pinned:   t.Pinned,
				})
			}
			dash.Columns = append(dash.Columns, dCol)
		}

		if task := m.kanban.SelectedTask(); task != nil {
			dash.SelectedTask = task.ID
		}

		s.Dashboard = dash
	}

	// Detail State
	if m.currentView == ViewDetail && m.detailView != nil && m.selectedTask != nil {
		s.Detail = &DebugDetail{
			TaskID:   m.selectedTask.ID,
			Title:    m.selectedTask.Title,
			Status:   m.selectedTask.Status,
			Project:  m.selectedTask.Project,
			Focused:  m.detailView.focused,
			Logs:     len(m.detailView.logs),
			HasPanes: m.detailView.hasActiveTmuxSession(),
		}
	}

	// Form State
	var formModel *FormModel
	if m.currentView == ViewNewTask {
		formModel = m.newTaskForm
	} else if m.currentView == ViewEditTask {
		formModel = m.editTaskForm
	}

	if formModel != nil {
		fields := make(map[string]string)
		fields["title"] = formModel.titleInput.Value()
		fields["body"] = formModel.bodyInput.Value()
		fields["attachments"] = formModel.attachmentsInput.Value()

		focusedField := ""
		switch formModel.focused {
		case FieldProject:
			focusedField = "project"
		case FieldTitle:
			focusedField = "title"
		case FieldBody:
			focusedField = "body"
		case FieldAttachments:
			focusedField = "attachments"
		case FieldType:
			focusedField = "type"
		case FieldExecutor:
			focusedField = "executor"
		}

		s.Form = &DebugForm{
			Title:        "Task Form",
			Fields:       fields,
			FocusedField: focusedField,
			Project:      formModel.project,
			Type:         formModel.taskType,
			Executor:     formModel.executor,
		}
		if m.currentView == ViewEditTask {
			s.Form.Title = "Edit Task"
		} else {
			s.Form.Title = "New Task"
		}
	}

	// Modals
	if m.currentView == ViewNewTaskConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "queue_confirm"}
	} else if m.currentView == ViewDeleteConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "delete_confirm"}
	} else if m.currentView == ViewCloseConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "close_confirm"}
	} else if m.currentView == ViewArchiveConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "archive_confirm"}
	} else if m.currentView == ViewQuitConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "quit_confirm"}
	} else if m.currentView == ViewProjectChangeConfirm {
		s.Modals = &DebugModals{ActiveConfirm: "project_change_confirm"}
	}

	// Filter
	if m.filterActive || m.filterText != "" {
		s.Filter = &DebugFilter{
			Active: m.filterActive,
			Text:   m.filterText,
		}
	}

	// Notification
	if m.notification != "" {
		s.Notification = &DebugNotification{
			Message: m.notification,
			TaskID:  m.notifyTaskID,
		}
	}

	return s
}

func viewName(v View) string {
	switch v {
	case ViewDashboard:
		return "dashboard"
	case ViewDetail:
		return "detail"
	case ViewNewTask:
		return "new_task"
	case ViewEditTask:
		return "edit_task"
	case ViewNewTaskConfirm:
		return "confirm_queue"
	case ViewDeleteConfirm:
		return "confirm_delete"
	case ViewCloseConfirm:
		return "confirm_close"
	case ViewArchiveConfirm:
		return "confirm_archive"
	case ViewQuitConfirm:
		return "confirm_quit"
	case ViewSettings:
		return "settings"
	case ViewRetry:
		return "retry"
	case ViewAttachments:
		return "attachments"
	case ViewChangeStatus:
		return "change_status"
	case ViewCommandPalette:
		return "command_palette"
	default:
		return "unknown"
	}
}

// DumpDebugStateToFile writes the current debug state to a file.
func (m *AppModel) DumpDebugStateToFile(path string) error {
	state := m.GenerateDebugState()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
