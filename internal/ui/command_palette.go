package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CommandPaletteModel represents the Command+P task switcher.
type CommandPaletteModel struct {
	db            *db.DB
	allTasks      []*db.Task
	filteredTasks []*db.Task
	projects      []*db.Project
	searchInput   textinput.Model
	selectedIndex int
	width         int
	height        int
	maxVisible    int

	// Result
	selectedTask *db.Task
	cancelled    bool
}

// NewCommandPaletteModel creates a new command palette model.
func NewCommandPaletteModel(database *db.DB, tasks []*db.Task, width, height int) *CommandPaletteModel {
	searchInput := textinput.New()
	searchInput.Placeholder = "Search tasks by title, ID, project, or PR URL/number..."
	searchInput.Focus()
	searchInput.CharLimit = 100
	searchInput.Width = min(60, width-10)

	// Load projects for project-based filtering
	projects, _ := database.ListProjects()

	m := &CommandPaletteModel{
		db:          database,
		allTasks:    tasks,
		projects:    projects,
		searchInput: searchInput,
		width:       width,
		height:      height,
		maxVisible:  10,
	}
	m.filterTasks()
	return m
}

// Init initializes the command palette.
func (m *CommandPaletteModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages.
func (m *CommandPaletteModel) Update(msg tea.Msg) (*CommandPaletteModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.cancelled = true
			return m, nil
		case "enter":
			if len(m.filteredTasks) > 0 && m.selectedIndex < len(m.filteredTasks) {
				m.selectedTask = m.filteredTasks[m.selectedIndex]
			}
			return m, nil
		case "up", "ctrl+p", "ctrl+k":
			if m.selectedIndex > 0 {
				m.selectedIndex--
			} else if len(m.filteredTasks) > 0 {
				// Wrap to bottom
				m.selectedIndex = len(m.filteredTasks) - 1
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			if m.selectedIndex < len(m.filteredTasks)-1 {
				m.selectedIndex++
			} else {
				// Wrap to top
				m.selectedIndex = 0
			}
			return m, nil
		case "pgup":
			m.selectedIndex -= m.maxVisible
			if m.selectedIndex < 0 {
				m.selectedIndex = 0
			}
			return m, nil
		case "pgdown":
			m.selectedIndex += m.maxVisible
			if m.selectedIndex >= len(m.filteredTasks) {
				m.selectedIndex = len(m.filteredTasks) - 1
			}
			if m.selectedIndex < 0 {
				m.selectedIndex = 0
			}
			return m, nil
		}

		// Update search input
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.filterTasks()
		return m, cmd
	}

	return m, nil
}

// scoredTask holds a task with its fuzzy match score for sorting
type scoredTask struct {
	task  *db.Task
	score int
}

// filterTasks filters tasks based on the search query using fuzzy matching.
// Results are sorted by match score, with best matches first.
// When a query is provided, it also searches the database to find older/done tasks.
func (m *CommandPaletteModel) filterTasks() {
	query := strings.TrimSpace(m.searchInput.Value())

	if query == "" {
		m.filteredTasks = m.allTasks
	} else {
		queryLower := strings.ToLower(query)

		// Collect all candidate tasks from local and database
		candidateTasks := make(map[int64]*db.Task)
		for _, task := range m.allTasks {
			candidateTasks[task.ID] = task
		}

		// Also search database for older/done tasks not in allTasks
		if m.db != nil {
			// Use a broader search to catch potential fuzzy matches
			// We'll filter and score them locally
			searchResults, err := m.db.SearchTasks(query, 100)
			if err == nil {
				for _, task := range searchResults {
					if _, exists := candidateTasks[task.ID]; !exists {
						candidateTasks[task.ID] = task
					}
				}
			}
		}

		// Score all tasks using fuzzy matching
		var scored []scoredTask
		for _, task := range candidateTasks {
			score := m.scoreTask(task, queryLower)
			if score >= 0 {
				scored = append(scored, scoredTask{task: task, score: score})
			}
		}

		// Sort by score descending (best matches first)
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})

		// Extract sorted tasks
		m.filteredTasks = make([]*db.Task, len(scored))
		for i, st := range scored {
			m.filteredTasks[i] = st.task
		}
	}

	// Clamp selected index
	if m.selectedIndex >= len(m.filteredTasks) {
		m.selectedIndex = max(0, len(m.filteredTasks)-1)
	}
}

// scoreTask calculates a fuzzy match score for a task against the query.
// Returns -1 if the task doesn't match, otherwise returns a positive score.
// Higher scores indicate better matches.
func (m *CommandPaletteModel) scoreTask(task *db.Task, query string) int {
	bestScore := -1

	// Check task ID (exact or prefix match gets high priority)
	idStr := fmt.Sprintf("%d", task.ID)
	if strings.HasPrefix(query, "#") {
		idQuery := strings.TrimPrefix(query, "#")
		if strings.Contains(idStr, idQuery) {
			return 1000 // ID matches are highest priority
		}
	} else if strings.Contains(idStr, query) {
		return 1000 // ID matches are highest priority
	}

	// Check PR number (high priority for specific lookups)
	if task.PRNumber > 0 {
		prNumStr := fmt.Sprintf("%d", task.PRNumber)
		prQuery := query
		if strings.HasPrefix(query, "#") {
			prQuery = strings.TrimPrefix(query, "#")
		}
		if strings.Contains(prNumStr, prQuery) {
			return 900 // PR number matches are high priority
		}
	}

	// Check PR URL
	if task.PRURL != "" {
		if strings.Contains(strings.ToLower(task.PRURL), query) {
			return 800 // PR URL matches
		}
	}

	// Fuzzy match on title (primary search field)
	titleScore := fuzzyScore(task.Title, query)
	if titleScore > bestScore {
		bestScore = titleScore
	}

	// Also check project name with fuzzy matching
	projectScore := fuzzyScore(task.Project, query)
	if projectScore > bestScore {
		// Project matches get a slight penalty vs title matches
		bestScore = projectScore - 50
	}

	// Check status as substring match
	if strings.Contains(strings.ToLower(task.Status), query) {
		statusScore := 100
		if statusScore > bestScore {
			bestScore = statusScore
		}
	}

	return bestScore
}

// matchesQuery checks if a task matches the search query.
func (m *CommandPaletteModel) matchesQuery(task *db.Task, query string) bool {
	// Check task ID
	if strings.Contains(fmt.Sprintf("%d", task.ID), query) {
		return true
	}
	// Check task ID with # prefix
	if strings.HasPrefix(query, "#") {
		idQuery := strings.TrimPrefix(query, "#")
		if strings.Contains(fmt.Sprintf("%d", task.ID), idQuery) {
			return true
		}
	}
	// Check title
	if strings.Contains(strings.ToLower(task.Title), query) {
		return true
	}
	// Check project
	if strings.Contains(strings.ToLower(task.Project), query) {
		return true
	}
	// Check status
	if strings.Contains(strings.ToLower(task.Status), query) {
		return true
	}
	// Check PR URL (e.g., "https://github.com/offerlab/offerlab/pull/2382")
	if task.PRURL != "" && strings.Contains(strings.ToLower(task.PRURL), query) {
		return true
	}
	// Check PR number (e.g., "2382" or "#2382")
	if task.PRNumber > 0 {
		prNumStr := fmt.Sprintf("%d", task.PRNumber)
		if strings.Contains(prNumStr, query) {
			return true
		}
		// Also match with # prefix
		if strings.HasPrefix(query, "#") {
			prQuery := strings.TrimPrefix(query, "#")
			if strings.Contains(prNumStr, prQuery) {
				return true
			}
		}
	}
	// Fuzzy match: check if all characters in query appear in order in title
	if fuzzyMatch(strings.ToLower(task.Title), query) {
		return true
	}
	return false
}

// fuzzyMatch performs a simple fuzzy match - all characters in pattern appear in order in str.
func fuzzyMatch(str, pattern string) bool {
	if len(pattern) == 0 {
		return true
	}
	if len(str) == 0 {
		return false
	}

	patternIdx := 0
	for i := 0; i < len(str) && patternIdx < len(pattern); i++ {
		if str[i] == pattern[patternIdx] {
			patternIdx++
		}
	}
	return patternIdx == len(pattern)
}

// fuzzyScore calculates a score for how well a pattern matches a string.
// Higher scores mean better matches. Returns -1 if pattern doesn't match.
// This implements VS Code-style fuzzy matching that favors:
// - Matches at word boundaries (start of words)
// - Consecutive character matches
// - Matches earlier in the string
// - Case-matching characters
func fuzzyScore(str, pattern string) int {
	if len(pattern) == 0 {
		return 0
	}
	if len(str) == 0 {
		return -1
	}

	strLower := strings.ToLower(str)
	patternLower := strings.ToLower(pattern)

	// First check if all pattern characters exist in order
	patternIdx := 0
	for i := 0; i < len(strLower) && patternIdx < len(patternLower); i++ {
		if strLower[i] == patternLower[patternIdx] {
			patternIdx++
		}
	}
	if patternIdx != len(patternLower) {
		return -1 // Pattern doesn't match
	}

	// Calculate score using dynamic programming to find the best match
	// We try to maximize the score by choosing optimal match positions
	return calculateBestScore(str, strLower, pattern, patternLower)
}

// calculateBestScore finds the best scoring match using a greedy algorithm
// that prefers word boundary matches and consecutive sequences
func calculateBestScore(str, strLower, pattern, patternLower string) int {
	const (
		bonusWordStart     = 50  // Match at start of a word
		bonusConsecutive   = 40  // Consecutive character match
		bonusFirstChar     = 25  // Match at first character of string
		bonusCamelCase     = 45  // Match at camelCase boundary
		bonusCaseMatch     = 5   // Exact case match
		penaltyUnmatched   = -3  // Each unmatched character before a match
		penaltyLeading     = -5  // Leading characters before first match (per char, max 3)
		maxLeadingPenalty  = -15 // Maximum leading penalty
	)

	score := 100 // Base score for matching
	patternIdx := 0
	prevMatchIdx := -1
	firstMatchIdx := -1

	for i := 0; i < len(strLower) && patternIdx < len(patternLower); i++ {
		if strLower[i] == patternLower[patternIdx] {
			// Found a match
			if firstMatchIdx == -1 {
				firstMatchIdx = i
			}

			// Bonus for matching at word start
			if isWordStart(str, i) {
				score += bonusWordStart
			}

			// Bonus for consecutive matches
			if prevMatchIdx >= 0 && i == prevMatchIdx+1 {
				score += bonusConsecutive
			}

			// Bonus for first character match
			if i == 0 {
				score += bonusFirstChar
			}

			// Bonus for camelCase boundary match
			if i > 0 && isCamelCaseBoundary(str, i) {
				score += bonusCamelCase
			}

			// Bonus for exact case match
			if str[i] == pattern[patternIdx] {
				score += bonusCaseMatch
			}

			// Penalty for gap between matches
			if prevMatchIdx >= 0 && i > prevMatchIdx+1 {
				gap := i - prevMatchIdx - 1
				score += gap * penaltyUnmatched
			}

			prevMatchIdx = i
			patternIdx++
		}
	}

	// Penalty for leading unmatched characters (capped)
	if firstMatchIdx > 0 {
		leadingPenalty := firstMatchIdx * penaltyLeading
		if leadingPenalty < maxLeadingPenalty {
			leadingPenalty = maxLeadingPenalty
		}
		score += leadingPenalty
	}

	return score
}

// isWordStart returns true if position i is at the start of a word
func isWordStart(str string, i int) bool {
	if i == 0 {
		return true
	}
	prev := str[i-1]
	curr := str[i]
	// Word start: after space, underscore, hyphen, or non-alpha followed by alpha
	if prev == ' ' || prev == '_' || prev == '-' || prev == '/' || prev == '.' {
		return true
	}
	// Start after a digit
	if prev >= '0' && prev <= '9' && (curr >= 'a' && curr <= 'z' || curr >= 'A' && curr <= 'Z') {
		return true
	}
	return false
}

// isCamelCaseBoundary returns true if position i is at a camelCase boundary
func isCamelCaseBoundary(str string, i int) bool {
	if i == 0 || i >= len(str) {
		return false
	}
	prev := str[i-1]
	curr := str[i]
	// Transition from lowercase to uppercase (camelCase)
	if prev >= 'a' && prev <= 'z' && curr >= 'A' && curr <= 'Z' {
		return true
	}
	return false
}

// View renders the command palette.
func (m *CommandPaletteModel) View() string {
	// Modal dimensions
	modalWidth := min(80, m.width-4)

	// Header
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("Go to Task")

	// Search input
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(0, 1).
		Width(modalWidth - 6)
	searchBox := inputStyle.Render(m.searchInput.View())

	// Task list
	var taskList strings.Builder
	if len(m.filteredTasks) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Padding(1, 0)
		taskList.WriteString(emptyStyle.Render("No tasks found"))
	} else {
		// Calculate visible range (for scrolling)
		start := 0
		end := len(m.filteredTasks)
		if end > m.maxVisible {
			// Center the selected item when possible
			halfVisible := m.maxVisible / 2
			start = m.selectedIndex - halfVisible
			if start < 0 {
				start = 0
			}
			end = start + m.maxVisible
			if end > len(m.filteredTasks) {
				end = len(m.filteredTasks)
				start = end - m.maxVisible
				if start < 0 {
					start = 0
				}
			}
		}

		// Show scroll indicator at top
		if start > 0 {
			scrollUp := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Render(fmt.Sprintf("  ... %d more above", start))
			taskList.WriteString(scrollUp + "\n")
		}

		// Render visible tasks
		for i := start; i < end; i++ {
			task := m.filteredTasks[i]
			isSelected := i == m.selectedIndex

			taskList.WriteString(m.renderTaskItem(task, isSelected, modalWidth-6))
			if i < end-1 {
				taskList.WriteString("\n")
			}
		}

		// Show scroll indicator at bottom
		remaining := len(m.filteredTasks) - end
		if remaining > 0 {
			scrollDown := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Render(fmt.Sprintf("\n  ... %d more below", remaining))
			taskList.WriteString(scrollDown)
		}
	}

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(ColorMuted).
		MarginTop(1)
	help := helpStyle.Render("Enter: select  Esc: cancel  " + IconArrowUp() + "/" + IconArrowDown() + ": navigate")

	// Combine all parts
	content := lipgloss.JoinVertical(lipgloss.Left,
		header,
		searchBox,
		"",
		taskList.String(),
		help,
	)

	// Modal box
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	modalContent := modalBox.Render(content)

	// Center on screen
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalContent)
}

// renderTaskItem renders a single task in the list.
func (m *CommandPaletteModel) renderTaskItem(task *db.Task, isSelected bool, width int) string {
	// Status icon
	statusIcon := StatusIcon(task.Status)
	statusColor := StatusColor(task.Status)

	// Build the line
	var line strings.Builder

	// Selection indicator
	if isSelected {
		line.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> "))
	} else {
		line.WriteString("  ")
	}

	// Status
	line.WriteString(lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon))
	line.WriteString(" ")

	// Task ID
	idStyle := lipgloss.NewStyle().Foreground(ColorMuted)
	line.WriteString(idStyle.Render(fmt.Sprintf("#%-4d", task.ID)))
	line.WriteString(" ")

	// Project tag
	if task.Project != "" {
		projectStyle := lipgloss.NewStyle().Foreground(ProjectColor(task.Project))
		shortProject := task.Project
		switch task.Project {
		case "offerlab":
			shortProject = "ol"
		case "influencekit":
			shortProject = "ik"
		}
		line.WriteString(projectStyle.Render("[" + shortProject + "]"))
		line.WriteString(" ")
	}

	// Title (truncate if needed)
	title := task.Title
	currentLen := lipgloss.Width(line.String())
	maxTitleLen := width - currentLen - 2
	if maxTitleLen < 10 {
		maxTitleLen = 10
	}
	if len(title) > maxTitleLen {
		title = title[:maxTitleLen-1] + "..."
	}

	titleStyle := lipgloss.NewStyle()
	if isSelected {
		titleStyle = titleStyle.Bold(true).Foreground(ColorPrimary)
	}
	line.WriteString(titleStyle.Render(title))

	return line.String()
}

// SelectedTask returns the selected task, or nil if cancelled.
func (m *CommandPaletteModel) SelectedTask() *db.Task {
	return m.selectedTask
}

// IsCancelled returns true if the user cancelled the palette.
func (m *CommandPaletteModel) IsCancelled() bool {
	return m.cancelled
}

// SetSize updates the command palette dimensions.
func (m *CommandPaletteModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.searchInput.Width = min(60, width-10)
}
