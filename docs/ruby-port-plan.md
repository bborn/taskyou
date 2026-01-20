# TaskYou Ruby Port: Comprehensive Plan

## Executive Summary

This document outlines a plan for porting TaskYou from Go to Ruby using the charm-ruby ecosystem. The analysis reveals both exciting opportunities and significant challenges.

**Verdict: Partially Feasible with Architecture Changes**

The charm-ruby ecosystem provides excellent coverage for the TUI components but lacks the SSH server capability (wish) that powers remote access. A successful port requires either architectural changes or building a custom SSH-to-TUI bridge.

---

## Current Go Application Analysis

### Statistics
- **52 Go files** (31 implementation + 21 test files)
- **~24,000 lines** of Go code (excluding tests)
- **8 major packages**: ui, db, executor, server, config, autocomplete, github, mcp, hooks

### Core Components

| Component | Go Library | Lines | Purpose |
|-----------|-----------|-------|---------|
| TUI Framework | charmbracelet/bubbletea | 8,000+ | Elm-architecture UI |
| UI Components | charmbracelet/bubbles | 2,000+ | Spinner, List, Input, etc. |
| Styling | charmbracelet/lipgloss | 1,500+ | Terminal colors/borders |
| Markdown | charmbracelet/glamour | 500+ | Render task descriptions |
| Forms | charmbracelet/huh | 1,000+ | Task creation forms |
| SSH Server | charmbracelet/wish | 113 | Serve TUI over SSH |
| CLI | spf13/cobra | 2,000+ | Command-line interface |
| Database | modernc.org/sqlite | 2,500+ | Pure-Go SQLite |

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         task CLI                                │
│  (cmd/task/main.go - Cobra commands)                           │
├─────────────────────────────────────────────────────────────────┤
│           │                              │                      │
│           ▼                              ▼                      │
│  ┌─────────────────┐           ┌─────────────────┐             │
│  │  Local Mode     │           │  Remote Mode    │             │
│  │  (-l flag)      │           │  (default)      │             │
│  │                 │           │                 │             │
│  │  Spawns local   │           │  SSH to remote  │             │
│  │  SSH server     │           │  taskd server   │             │
│  └────────┬────────┘           └────────┬────────┘             │
│           │                              │                      │
│           └──────────────┬───────────────┘                      │
│                          ▼                                      │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │                     taskd Daemon                          │ │
│  │  (cmd/taskd/main.go)                                      │ │
│  ├───────────────────────────────────────────────────────────┤ │
│  │  SSH Server (Wish)  │  Executor  │  Database (SQLite)     │ │
│  │  Port 2222          │            │                        │ │
│  │                     │  - Claude  │  - Tasks               │ │
│  │  Serves Bubble Tea  │  - Codex   │  - Projects            │ │
│  │  TUI over SSH       │  - Hooks   │  - Memories            │ │
│  └───────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## Charm Ruby Ecosystem Analysis

### Available Ruby Gems (as of January 2026)

| Gem | Version | Stars | Status | Go Equivalent |
|-----|---------|-------|--------|---------------|
| bubbletea-ruby | v0.1.0 | 102 | Active | charmbracelet/bubbletea |
| bubbles-ruby | - | 58 | Active | charmbracelet/bubbles |
| lipgloss-ruby | - | 46 | Active | charmbracelet/lipgloss |
| glamour-ruby | - | 43 | Active | charmbracelet/glamour |
| huh-ruby | - | 39 | Active | charmbracelet/huh |
| gum-ruby | - | 53 | Active | charmbracelet/gum |
| harmonica-ruby | - | 38 | Active | charmbracelet/harmonica |
| ntcharts-ruby | - | 34 | Active | charmbracelet/ntcharts |
| bubblezone-ruby | - | 33 | Active | charmbracelet/bubblezone |

### Critical Gap: No wish-ruby

**The charm-ruby ecosystem does NOT include a port of Wish (SSH server for TUI apps).**

This is the most significant blocker because:
1. `taskd` serves the Bubble Tea TUI over SSH
2. Remote access is a core feature of TaskYou
3. Building a custom SSH-TUI bridge is non-trivial

---

## Porting Strategy Options

### Option A: Full Port with Custom SSH Bridge (Recommended)

Build a custom Ruby SSH server using `net-ssh` that integrates with bubbletea-ruby.

**Pros:**
- Complete feature parity
- True Ruby port
- Full control over implementation

**Cons:**
- Requires building wish-equivalent
- Higher initial complexity
- ~2-4 weeks additional effort

**Implementation:**
```ruby
# Conceptual SSH-TUI bridge
require 'net-ssh'
require 'bubbletea'

class TaskServer
  def handle_session(session)
    app = TaskApp.new(db, executor)
    pty = session.request_pty(term: 'xterm-256color')

    # Bridge session I/O to Bubble Tea
    Bubbletea.run(app,
      input: session.io,
      output: session.io
    )
  end
end
```

### Option B: Local-Only Port

Remove SSH remote functionality; run TUI locally only.

**Pros:**
- Simpler implementation
- No custom SSH work needed
- Can be done with existing charm-ruby

**Cons:**
- Loss of remote access feature
- Different user experience
- Deviation from original architecture

### Option C: Hybrid Approach

Keep Go daemon for SSH serving; port CLI and TUI models to Ruby.

**Pros:**
- Leverages existing stable SSH code
- Incremental migration path
- Lower risk

**Cons:**
- Two languages to maintain
- Inter-language communication complexity
- Not a "pure" Ruby port

---

## Recommended Approach: Option A (Full Port)

Given the requirement for verifiable correctness, Option A provides:
1. Clean architecture for testing
2. Complete feature parity for verification
3. Single language codebase

---

## Component Mapping

### Direct Mappings (Easy)

| Go Package | Ruby Equivalent | Effort |
|------------|-----------------|--------|
| `charmbracelet/bubbletea` | `bubbletea` gem | Low |
| `charmbracelet/bubbles` | `bubbles` gem | Low |
| `charmbracelet/lipgloss` | `lipgloss` gem | Low |
| `charmbracelet/glamour` | `glamour` gem | Low |
| `charmbracelet/huh` | `huh` gem | Low |
| `modernc.org/sqlite` | `sqlite3` gem | Low |
| `gopkg.in/yaml.v3` | `yaml` stdlib | Low |

### Requires Custom Work

| Go Package | Ruby Approach | Effort |
|------------|---------------|--------|
| `charmbracelet/wish` | Custom `net-ssh` integration | High |
| `spf13/cobra` | `thor` or `dry-cli` gem | Medium |
| `charmbracelet/log` | `logger` stdlib or `semantic_logger` | Low |
| Process management | Ruby `Process` + `PTY` | Medium |

### Feature-Specific Mappings

| Feature | Go Implementation | Ruby Approach |
|---------|-------------------|---------------|
| tmux integration | `exec.Command("tmux", ...)` | `Open3.popen3("tmux", ...)` |
| Git worktrees | `exec.Command("git", ...)` | `rugged` gem or shell |
| Claude CLI | `exec.Command("claude", ...)` | `Open3.popen3("claude", ...)` |
| GitHub API | `net/http` + JSON parsing | `octokit` gem |
| Fuzzy search | `sahilm/fuzzy` | `fuzzystringmatch` gem |

---

## File-by-File Porting Guide

### Phase 1: Core Infrastructure (Week 1-2)

| Go File | Ruby File | Priority |
|---------|-----------|----------|
| `internal/db/sqlite.go` | `lib/taskyou/db/database.rb` | P0 |
| `internal/db/tasks.go` | `lib/taskyou/db/task.rb` | P0 |
| `internal/config/config.go` | `lib/taskyou/config.rb` | P0 |
| `internal/hooks/hooks.go` | `lib/taskyou/hooks.rb` | P1 |

### Phase 2: Executor System (Week 2-3)

| Go File | Ruby File | Priority |
|---------|-----------|----------|
| `internal/executor/executor.go` | `lib/taskyou/executor/executor.rb` | P0 |
| `internal/executor/task_executor.go` | `lib/taskyou/executor/task_executor.rb` | P0 |
| `internal/executor/claude_executor.go` | `lib/taskyou/executor/claude_executor.rb` | P0 |
| `internal/executor/codex_executor.go` | `lib/taskyou/executor/codex_executor.rb` | P1 |
| `internal/executor/project_config.go` | `lib/taskyou/executor/project_config.rb` | P1 |
| `internal/executor/memory_extractor.go` | `lib/taskyou/executor/memory_extractor.rb` | P2 |

### Phase 3: UI Components (Week 3-5)

| Go File | Ruby File | Priority |
|---------|-----------|----------|
| `internal/ui/app.go` | `lib/taskyou/ui/app.rb` | P0 |
| `internal/ui/kanban.go` | `lib/taskyou/ui/kanban.rb` | P0 |
| `internal/ui/detail.go` | `lib/taskyou/ui/detail.rb` | P0 |
| `internal/ui/form.go` | `lib/taskyou/ui/form.rb` | P0 |
| `internal/ui/styles.go` | `lib/taskyou/ui/styles.rb` | P0 |
| `internal/ui/theme.go` | `lib/taskyou/ui/theme.rb` | P1 |
| `internal/ui/settings.go` | `lib/taskyou/ui/settings.rb` | P1 |
| `internal/ui/command_palette.go` | `lib/taskyou/ui/command_palette.rb` | P1 |
| `internal/ui/memories.go` | `lib/taskyou/ui/memories.rb` | P2 |

### Phase 4: Server & CLI (Week 5-6)

| Go File | Ruby File | Priority |
|---------|-----------|----------|
| `internal/server/ssh.go` | `lib/taskyou/server/ssh_server.rb` | P0 |
| `cmd/task/main.go` | `exe/task` | P0 |
| `cmd/taskd/main.go` | `exe/taskd` | P0 |

### Phase 5: Auxiliary Features (Week 6-7)

| Go File | Ruby File | Priority |
|---------|-----------|----------|
| `internal/autocomplete/autocomplete.go` | `lib/taskyou/autocomplete.rb` | P2 |
| `internal/github/pr.go` | `lib/taskyou/github/pr.rb` | P2 |
| `internal/mcp/server.go` | `lib/taskyou/mcp/server.rb` | P2 |

---

## Verification Strategy

This is critical for ensuring the Ruby port has complete parity with the Go original.

### 1. Unit Test Parity

Port all 21 Go test files to Ruby RSpec tests:

```ruby
# Example: spec/lib/taskyou/db/task_spec.rb
RSpec.describe Taskyou::DB::Task do
  describe '#create' do
    it 'creates a task with default project' do
      task = described_class.create(title: 'Test')
      expect(task.project).to eq('personal')
    end

    it 'validates project exists' do
      expect {
        described_class.create(title: 'Test', project: 'nonexistent')
      }.to raise_error(Taskyou::DB::ProjectNotFoundError)
    end
  end
end
```

### 2. Integration Test Suite

Create end-to-end tests that verify behavior matches:

```ruby
# spec/integration/task_workflow_spec.rb
RSpec.describe 'Task Workflow', :integration do
  it 'creates task, assigns to executor, completes' do
    # Create task via CLI
    output = `./exe/task add "Test task" --project personal`
    expect(output).to include('Created task')

    # Verify database state
    task = Taskyou::DB::Task.last
    expect(task.status).to eq('backlog')

    # Queue task
    task.update(status: 'queued')

    # Wait for executor
    sleep 2
    task.reload
    expect(task.status).to eq('processing')
  end
end
```

### 3. Behavioral Comparison Tests

Run both Go and Ruby versions side-by-side and compare outputs:

```ruby
# spec/parity/parity_spec.rb
RSpec.describe 'Go/Ruby Parity' do
  GO_BINARY = './bin/task'
  RUBY_BINARY = './exe/task'

  describe 'task list' do
    it 'produces identical output' do
      go_output = `#{GO_BINARY} list --format json`
      ruby_output = `#{RUBY_BINARY} list --format json`

      go_data = JSON.parse(go_output)
      ruby_data = JSON.parse(ruby_output)

      expect(ruby_data).to eq(go_data)
    end
  end

  describe 'task add' do
    it 'creates equivalent database records' do
      # Use shared database
      `#{GO_BINARY} add "Go task" --project test`
      `#{RUBY_BINARY} add "Ruby task" --project test`

      tasks = Taskyou::DB::Task.where(project: 'test')
      expect(tasks.map(&:title)).to contain_exactly('Go task', 'Ruby task')
    end
  end
end
```

### 4. Database Schema Compatibility

Ensure Ruby uses the exact same SQLite schema:

```ruby
# spec/db/schema_spec.rb
RSpec.describe 'Database Schema' do
  it 'matches Go schema exactly' do
    go_schema = File.read('internal/db/migrations.sql')
    ruby_schema = Taskyou::DB::Database.schema_sql

    # Normalize whitespace and compare
    expect(normalize(ruby_schema)).to eq(normalize(go_schema))
  end

  it 'can read Go-created database' do
    # Create DB with Go
    `#{GO_BINARY} add "Test" --db /tmp/test.db`

    # Read with Ruby
    db = Taskyou::DB::Database.new('/tmp/test.db')
    tasks = db.list_tasks

    expect(tasks.first.title).to eq('Test')
  end
end
```

### 5. TUI Visual Regression Tests

Capture terminal output and compare:

```ruby
# spec/ui/visual_regression_spec.rb
RSpec.describe 'TUI Visual Regression' do
  it 'renders kanban board identically' do
    go_render = capture_tui_output(GO_BINARY)
    ruby_render = capture_tui_output(RUBY_BINARY)

    # Allow minor ANSI differences but content must match
    expect(strip_ansi(ruby_render)).to eq(strip_ansi(go_render))
  end
end
```

### 6. Automated Verification Script

```bash
#!/bin/bash
# scripts/verify-parity.sh

set -e

echo "Building Go binary..."
make build-go

echo "Building Ruby gem..."
bundle exec rake build

echo "Running unit tests..."
bundle exec rspec spec/lib

echo "Running integration tests..."
bundle exec rspec spec/integration

echo "Running parity tests..."
bundle exec rspec spec/parity

echo "Running visual regression tests..."
bundle exec rspec spec/ui

echo "✅ All verification passed!"
```

---

## Project Structure (Ruby)

```
taskyou/
├── Gemfile
├── Rakefile
├── taskyou.gemspec
├── exe/
│   ├── task                    # CLI entry point
│   └── taskd                   # Daemon entry point
├── lib/
│   ├── taskyou.rb
│   └── taskyou/
│       ├── version.rb
│       ├── config.rb
│       ├── hooks.rb
│       ├── db/
│       │   ├── database.rb
│       │   ├── task.rb
│       │   ├── project.rb
│       │   └── memory.rb
│       ├── executor/
│       │   ├── executor.rb
│       │   ├── task_executor.rb
│       │   ├── claude_executor.rb
│       │   ├── codex_executor.rb
│       │   ├── project_config.rb
│       │   └── memory_extractor.rb
│       ├── ui/
│       │   ├── app.rb
│       │   ├── kanban.rb
│       │   ├── detail.rb
│       │   ├── form.rb
│       │   ├── styles.rb
│       │   ├── theme.rb
│       │   ├── settings.rb
│       │   ├── command_palette.rb
│       │   └── memories.rb
│       ├── server/
│       │   └── ssh_server.rb
│       ├── autocomplete/
│       │   └── autocomplete.rb
│       ├── github/
│       │   └── pr.rb
│       └── mcp/
│           └── server.rb
└── spec/
    ├── spec_helper.rb
    ├── lib/                    # Unit tests
    ├── integration/            # E2E tests
    ├── parity/                 # Go/Ruby comparison
    └── ui/                     # Visual regression
```

---

## Dependencies (Gemfile)

```ruby
# frozen_string_literal: true

source 'https://rubygems.org'

gem 'bubbletea', '~> 0.1'
gem 'bubbles', '~> 0.1'
gem 'lipgloss', '~> 0.1'
gem 'glamour', '~> 0.1'
gem 'huh', '~> 0.1'

gem 'sqlite3', '~> 2.0'
gem 'net-ssh', '~> 7.2'
gem 'thor', '~> 1.3'
gem 'octokit', '~> 9.0'
gem 'fuzzystringmatch', '~> 1.0'

group :development, :test do
  gem 'rspec', '~> 3.13'
  gem 'rubocop', '~> 1.66'
  gem 'rubocop-rspec', '~> 3.0'
end
```

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| charm-ruby immaturity | Medium | High | Pin versions, contribute fixes upstream |
| SSH bridge complexity | High | High | Prototype early, consider Option C fallback |
| Performance degradation | Medium | Medium | Benchmark critical paths, optimize |
| tmux integration differences | Low | Medium | Test thoroughly on macOS/Linux |
| Test coverage gaps | Medium | High | Port all Go tests, add integration tests |

---

## Timeline Estimate

| Phase | Duration | Deliverable |
|-------|----------|-------------|
| Phase 1: Infrastructure | 2 weeks | Database + Config working |
| Phase 2: Executor | 1.5 weeks | Task execution working |
| Phase 3: UI | 2 weeks | TUI functional |
| Phase 4: Server + CLI | 1.5 weeks | SSH server + commands |
| Phase 5: Auxiliary | 1 week | Autocomplete, GitHub, MCP |
| Phase 6: Testing | 1 week | Full parity verification |

**Total: ~9 weeks** for complete port with verification

---

## Success Criteria

The Ruby port is considered complete when:

1. **All 21 Go test files have Ruby equivalents** passing
2. **Parity tests confirm identical behavior** for:
   - Task CRUD operations
   - Executor task processing
   - TUI rendering (content-wise)
   - CLI command outputs
3. **Integration tests pass** for full workflows
4. **Database interoperability** works (Go can read Ruby DB, vice versa)
5. **SSH remote access works** with Ruby daemon
6. **Performance is acceptable** (no >2x degradation)

---

## Next Steps

1. **Prototype SSH Bridge** - Validate `net-ssh` + bubbletea integration
2. **Setup Ruby Project Structure** - Create gem scaffold
3. **Port Database Layer** - Easiest starting point, enables testing
4. **Iterative TUI Development** - Build component by component
5. **Continuous Parity Testing** - Verify each component against Go

---

## Appendix: Key Code Translations

### Example: Bubble Tea Model

**Go:**
```go
type Model struct {
    count int
}

func (m Model) Init() tea.Cmd {
    return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "up":
            m.count++
        case "down":
            m.count--
        }
    }
    return m, nil
}

func (m Model) View() string {
    return fmt.Sprintf("Count: %d", m.count)
}
```

**Ruby:**
```ruby
class Counter
  include Bubbletea::Model

  def initialize
    @count = 0
  end

  def update(message)
    case message
    when Bubbletea::KeyMessage
      case message.to_s
      when 'up'
        @count += 1
      when 'down'
        @count -= 1
      end
    end
    [self, nil]
  end

  def view
    "Count: #{@count}"
  end
end
```

### Example: Database Query

**Go:**
```go
func (db *DB) GetTask(id int64) (*Task, error) {
    t := &Task{}
    err := db.QueryRow(`
        SELECT id, title, status FROM tasks WHERE id = ?
    `, id).Scan(&t.ID, &t.Title, &t.Status)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    return t, err
}
```

**Ruby:**
```ruby
def get_task(id)
  row = @db.execute('SELECT id, title, status FROM tasks WHERE id = ?', id).first
  return nil if row.nil?

  Task.new(id: row[0], title: row[1], status: row[2])
end
```
