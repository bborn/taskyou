# TaskYou MCP Tools Reference

TaskYou provides Model Context Protocol (MCP) tools that allow AI agents to interact with the task management system programmatically.

## Core Task Management

### taskyou_complete

Mark the current task as complete.

**Parameters:**
- `summary` (string, required) - Brief summary of what was accomplished

**Example:**
```json
{
  "name": "taskyou_complete",
  "arguments": {
    "summary": "Implemented project context caching feature and updated documentation"
  }
}
```

### taskyou_needs_input

Request input from the user when you need clarification.

**Parameters:**
- `question` (string, required) - The question to ask the user

**Example:**
```json
{
  "name": "taskyou_needs_input",
  "arguments": {
    "question": "Should I use TypeScript or JavaScript for the new component?"
  }
}
```

**Effect:** Sets task status to "blocked" and notifies the user.

## Project Context (Memory Files)

### taskyou_get_project_context

Get cached project context to skip redundant codebase exploration.

**Parameters:** None

**Returns:** Cached project context or empty string if not set

**Example:**
```json
{
  "name": "taskyou_get_project_context",
  "arguments": {}
}
```

**Response:**
```
## Cached Project Context

This is a Go project using:
- Bubble Tea for TUI framework
- SQLite for data storage
- Charm libraries for styling

Key directories:
- internal/db/ - Database layer and schema
- internal/executor/ - Task execution engine
- internal/ui/ - TUI components
...
```

**Best Practice:** Call this FIRST at the start of every task. If context exists, use it to skip exploration.

### taskyou_set_project_context

Save auto-generated project context for future tasks.

**Parameters:**
- `context` (string, required) - The project context to cache

**Example:**
```json
{
  "name": "taskyou_set_project_context",
  "arguments": {
    "context": "This is a Go project using Bubble Tea for TUI...\n\nKey directories:\n- internal/db/\n- internal/executor/\n..."
  }
}
```

**What to include:**
- Project structure and key directories
- Tech stack and frameworks used
- Coding conventions and patterns
- Important files and their purposes
- Common development workflows

**When to call:** After exploring a codebase for the first time, save your findings so future tasks can skip exploration.

## Task Management

### taskyou_create_task

Create a new task in the system.

**Parameters:**
- `title` (string, required) - Title of the task
- `body` (string, optional) - Detailed description
- `project` (string, optional) - Project name (defaults to current project)
- `type` (string, optional) - Task type (code, writing, thinking)
- `status` (string, optional) - Initial status (backlog, queued, defaults to backlog)

**Example:**
```json
{
  "name": "taskyou_create_task",
  "arguments": {
    "title": "Add unit tests for executor",
    "body": "Create comprehensive test coverage for the executor package",
    "type": "code",
    "status": "backlog"
  }
}
```

### taskyou_list_tasks

List active tasks in the project.

**Parameters:**
- `status` (string, optional) - Filter by status (queued, processing, blocked, backlog)
- `limit` (integer, optional) - Maximum tasks to return (default: 10, max: 50)
- `project` (string, optional) - Filter by project (defaults to current project)

**Example:**
```json
{
  "name": "taskyou_list_tasks",
  "arguments": {
    "status": "blocked",
    "limit": 5
  }
}
```

### taskyou_show_task

Get details of a specific past task by ID.

**Parameters:**
- `task_id` (integer, required) - The ID of the task to retrieve

**Example:**
```json
{
  "name": "taskyou_show_task",
  "arguments": {
    "task_id": 42
  }
}
```

**Note:** Only works for tasks in the same project (enforces project isolation).

## Screenshots & Attachments

### taskyou_screenshot

Take a screenshot of the entire screen and save it as an attachment.

**Parameters:**
- `filename` (string, optional) - Filename for the screenshot (defaults to screenshot-{timestamp}.png)
- `description` (string, optional) - Description of what the screenshot shows

**Example:**
```json
{
  "name": "taskyou_screenshot",
  "arguments": {
    "filename": "new-kanban-ui.png",
    "description": "Updated kanban board with project context indicator"
  }
}
```

**Supported platforms:**
- macOS: Uses `screencapture`
- Linux: Uses `gnome-screenshot`, `scrot`, or `import` (ImageMagick)

**Use cases:**
- Documenting UI changes
- Capturing visual bugs
- Showing frontend work in PRs
- Recording test results

## Usage Patterns

### Starting a New Task

```
1. taskyou_get_project_context()
   → If context exists, use it to understand codebase
   → If empty, proceed to step 2

2. [Explore codebase]

3. taskyou_set_project_context("...")
   → Save findings for future tasks

4. [Work on the task]

5. taskyou_screenshot() (if visual work)
   → Document UI changes

6. taskyou_complete(summary="...")
   → Mark task complete
```

### Task Needs User Input

```
1. [Working on task]

2. taskyou_needs_input("Should I use approach A or B?")
   → Task marked as "blocked"
   → User notified via UI

3. [User provides feedback via retry]

4. [Continue with task]

5. taskyou_complete(summary="...")
```

### Breaking Down Work

```
1. [Working on large task]

2. taskyou_create_task(
     title="Subtask: Add tests",
     body="Create unit tests for new feature"
   )

3. taskyou_create_task(
     title="Subtask: Update docs",
     body="Document new API endpoints"
   )

4. taskyou_complete(summary="Completed main implementation, created follow-up tasks")
```

## Tips & Best Practices

### Project Context
- **Always check first:** Call `taskyou_get_project_context` at the start of every task
- **Be comprehensive:** Include structure, tech stack, patterns, conventions
- **Keep current:** Update when the codebase significantly changes
- **Use markdown:** Format context with headings and lists for readability

### Screenshots
- Take screenshots of visual changes before completing frontend tasks
- Use descriptive filenames to make them easy to find
- Add descriptions to provide context for reviewers

### Task Creation
- Break down large tasks into smaller subtasks
- Use descriptive titles and detailed bodies
- Set appropriate task types for better organization

### Completing Tasks
- Provide meaningful summaries in `taskyou_complete`
- Include what was done, not just "task complete"
- Mention any follow-up items or blockers encountered

## See Also

- [AGENTS.md](../AGENTS.md) - Agent guidelines and architecture
- [Analysis: Boris Cherny's Recommendations](analysis-boris-cherny-recommendations.md) - Background on project context feature
- [MCP Implementation](../internal/mcp/server.go) - Source code for MCP tools
