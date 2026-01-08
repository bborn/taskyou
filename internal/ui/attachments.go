package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bborn/workflow/internal/db"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AttachmentsModel displays and manages task attachments.
type AttachmentsModel struct {
	db          *db.DB
	task        *db.Task
	attachments []*db.Attachment
	cursor      int
	width       int
	height      int
	err         error
}

// NewAttachmentsModel creates a new attachments view.
func NewAttachmentsModel(task *db.Task, database *db.DB, width, height int) *AttachmentsModel {
	m := &AttachmentsModel{
		db:     database,
		task:   task,
		width:  width,
		height: height,
	}
	m.loadAttachments()
	return m
}

func (m *AttachmentsModel) loadAttachments() {
	attachments, err := m.db.ListAttachments(m.task.ID)
	if err != nil {
		m.err = err
		return
	}
	m.attachments = attachments
}

// Init initializes the model.
func (m *AttachmentsModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *AttachmentsModel) Update(msg tea.Msg) (*AttachmentsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.attachments)-1 {
				m.cursor++
			}
		case "enter":
			// Open selected attachment
			if len(m.attachments) > 0 {
				return m, m.openAttachment(m.attachments[m.cursor])
			}
		case "d":
			// Delete selected attachment
			if len(m.attachments) > 0 {
				return m, m.deleteAttachment(m.attachments[m.cursor].ID)
			}
		case "a":
			// Add attachment via file picker
			return m, m.pickFile()
		}
	case attachmentAddedMsg:
		m.loadAttachments()
	case attachmentDeletedMsg:
		m.loadAttachments()
		if m.cursor >= len(m.attachments) && m.cursor > 0 {
			m.cursor--
		}
	}
	return m, nil
}

// View renders the attachments view.
func (m *AttachmentsModel) View() string {
	var b strings.Builder

	title := Title.Render(fmt.Sprintf("Attachments - Task #%d", m.task.ID))
	b.WriteString(title + "\n\n")

	if m.err != nil {
		b.WriteString(Error.Render(m.err.Error()))
		return b.String()
	}

	if len(m.attachments) == 0 {
		b.WriteString(Dim.Render("No attachments. Press 'a' to add one.\n"))
	} else {
		for i, att := range m.attachments {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}

			// Format size
			size := formatSize(att.Size)

			line := fmt.Sprintf("%s%s (%s)", cursor, att.Filename, size)
			if i == m.cursor {
				line = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	help := Dim.Render("a: add • enter: open • d: delete • q: back")
	b.WriteString(help)

	return b.String()
}

func (m *AttachmentsModel) openAttachment(att *db.Attachment) tea.Cmd {
	return func() tea.Msg {
		// Get full attachment with data
		fullAtt, err := m.db.GetAttachment(att.ID)
		if err != nil {
			return attachmentErrorMsg{err: err}
		}

		// Write to temp file
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, fullAtt.Filename)
		if err := os.WriteFile(tmpFile, fullAtt.Data, 0644); err != nil {
			return attachmentErrorMsg{err: err}
		}

		// Open with system default
		exec.Command("open", tmpFile).Start()
		return nil
	}
}

func (m *AttachmentsModel) deleteAttachment(id int64) tea.Cmd {
	return func() tea.Msg {
		err := m.db.DeleteAttachment(id)
		return attachmentDeletedMsg{err: err}
	}
}

func (m *AttachmentsModel) pickFile() tea.Cmd {
	// Use osascript to open file picker on macOS
	return tea.ExecProcess(
		exec.Command("osascript", "-e", `POSIX path of (choose file)`),
		func(err error) tea.Msg {
			return filePickerDoneMsg{err: err}
		},
	)
}

// AddFile adds a file to the task's attachments.
func (m *AttachmentsModel) AddFile(path string) tea.Cmd {
	return func() tea.Msg {
		// Read file
		data, err := os.ReadFile(path)
		if err != nil {
			return attachmentErrorMsg{err: err}
		}

		// Detect mime type (basic)
		mimeType := detectMimeType(path)

		// Add to database
		_, err = m.db.AddAttachment(m.task.ID, filepath.Base(path), mimeType, data)
		if err != nil {
			return attachmentErrorMsg{err: err}
		}

		return attachmentAddedMsg{}
	}
}

// Message types
type attachmentAddedMsg struct{}
type attachmentDeletedMsg struct{ err error }
type attachmentErrorMsg struct{ err error }
type filePickerDoneMsg struct{ err error }

// Helper functions
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func detectMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mimeTypes := map[string]string{
		".txt":  "text/plain",
		".md":   "text/markdown",
		".json": "application/json",
		".xml":  "application/xml",
		".html": "text/html",
		".css":  "text/css",
		".js":   "application/javascript",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".pdf":  "application/pdf",
		".zip":  "application/zip",
		".go":   "text/x-go",
		".py":   "text/x-python",
		".rb":   "text/x-ruby",
		".rs":   "text/x-rust",
	}
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}
