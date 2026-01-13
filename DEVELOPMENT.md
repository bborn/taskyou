# Development Best Practices

Guide for AI-assisted development in this repository. Since all code is written with AI assistance, following these practices helps maintain quality and avoid technical debt.

## Core Principles

### 1. AI as Collaborator, Not Automation

Treat AI-generated code like work from a capable but context-limited junior developer:

- **Always review generated code** line by line before committing
- **Understand what you're committing** - if you can't explain it, don't merge it
- **Question complexity** - 9 out of 10 times, AI suggests overly complicated approaches. Ask for simpler alternatives
- **Validate logic, not just syntax** - AI produces syntactically correct but logically flawed code regularly

### 2. Plan Before Implementation

Don't start coding immediately:

1. Request architectural options first
2. Discuss trade-offs before writing code
3. Define acceptance criteria upfront
4. Break large tasks into smaller, reviewable chunks

### 3. Provide Rich Context

Poor context yields poor results. Always include:

- Relevant existing code files
- Exact error messages and stack traces
- Database schemas and API contracts
- Version numbers and environment details
- Examples of desired patterns from the codebase

## Code Quality Standards

### Go Conventions

This project follows standard Go conventions:

```bash
# Format code
go fmt ./...

# Run linter (configured in .golangci.yml)
golangci-lint run

# Run tests
go test ./...
```

### Commit Messages

Use conventional commits format:

```
<type>: <description>

Types:
  feat:     New feature
  fix:      Bug fix
  refactor: Code reorganization (no behavior change)
  docs:     Documentation only
  test:     Adding or updating tests
  chore:    Maintenance tasks
  perf:     Performance improvements
```

Examples from this repo:
- `feat: add keyboard shortcut to toggle dangerous mode`
- `fix: prefer stored daemon_session when finding task tmux window`
- `refactor: extract memory injection into separate function`

### Code Organization

Follow the existing package structure:

```
internal/
├── config/      # Configuration management
├── db/          # Database layer (CRUD, queries)
├── executor/    # Background task processing
├── github/      # GitHub API integration
├── hooks/       # Task lifecycle hooks
├── mcp/         # Model Context Protocol
├── server/      # SSH server
└── ui/          # Terminal UI components
```

**Package Guidelines:**
- Keep packages focused on a single responsibility
- Use `internal/` for non-exported packages
- Place tests alongside source files (`*_test.go`)

## Testing Requirements

### When to Write Tests

- **Database operations**: All CRUD functions need tests
- **Business logic**: Executor logic, state transitions
- **Parsing/transformation**: URL parsing, input detection
- **Bug fixes**: Add a test that would have caught the bug

### Test Patterns

Follow the existing patterns in this codebase:

```go
func TestFeatureName(t *testing.T) {
    // Setup: Create temporary database
    tmpDir := t.TempDir()
    dbPath := filepath.Join(tmpDir, "test.db")

    db, err := Open(dbPath)
    if err != nil {
        t.Fatalf("failed to open database: %v", err)
    }
    defer db.Close()

    // Test the feature
    result, err := db.SomeOperation()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Assert expectations
    if result != expected {
        t.Errorf("expected %v, got %v", expected, result)
    }
}
```

### Running Tests

```bash
# Run all tests
make test

# Run specific package tests
go test ./internal/db/...

# Run with verbose output
go test -v ./...

# Run specific test
go test -run TestFeatureName ./internal/db/
```

## Security Practices

### Code Review Checklist

Before merging AI-generated code, verify:

- [ ] **No hardcoded secrets** - API keys, passwords, tokens
- [ ] **Input validation** - User inputs sanitized before use
- [ ] **SQL injection prevention** - Using parameterized queries
- [ ] **Error messages** - Don't leak sensitive information
- [ ] **File operations** - Validate paths, prevent traversal

### What AI Often Gets Wrong

Watch for these common AI security mistakes:

1. **Hardcoded credentials** in examples that get committed
2. **Missing input validation** on user-provided data
3. **Overly permissive error handling** that swallows important errors
4. **Insecure defaults** that should be opt-in

## Avoiding Technical Debt

### The 70% Problem

AI gets you 70% of the way quickly, but the final 30% requires careful engineering. Watch for:

- Bug fixes that create new bugs
- Accumulated complexity from quick fixes
- Missing edge case handling
- Inconsistent patterns across the codebase

### Prevention Strategies

1. **Small, focused changes** - Easier to review and less likely to introduce issues
2. **Incremental testing** - Test after each change, not at the end
3. **Pattern consistency** - Match existing code style and patterns
4. **Explicit over clever** - Readable code beats "clever" solutions

### Code Smell Detection

Question AI-generated code that:

- Adds dependencies for simple tasks
- Creates abstractions for single-use cases
- Uses complex patterns where simple ones work
- Includes commented-out code or TODOs
- Has functions longer than ~50 lines

## Workflow Guidelines

### Before Starting a Task

1. Read relevant existing code to understand patterns
2. Check for similar implementations in the codebase
3. Identify files that will be modified
4. Consider impact on other components

### During Implementation

1. Make changes incrementally
2. Run tests frequently (`make test`)
3. Run linter before committing (`golangci-lint run`)
4. Keep commits atomic and focused

### Before Submitting a PR

1. **Self-review** - Read through all changes as a reviewer would
2. **Run full test suite** - `make test`
3. **Check linting** - `golangci-lint run`
4. **Test manually** - Run the app and verify behavior
5. **Update documentation** - If behavior changes, update docs

### PR Description Template

```markdown
## Summary
Brief description of what changed and why.

## Changes
- List of specific changes made

## Testing
- How the changes were tested
- Any manual testing steps

## Screenshots (if UI changes)
```

## Error Handling

### Go Error Patterns

Follow Go's explicit error handling:

```go
// Good: Check errors explicitly
result, err := doSomething()
if err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}

// Bad: Ignoring errors silently
result, _ := doSomething()  // Don't do this unless intentional
```

### When to Ignore Errors

Some errors are intentionally ignored (configured in `.golangci.yml`):

- Cleanup operations (closing files, connections)
- Logging operations
- Fire-and-forget calls where failure is acceptable

Document why when ignoring errors:

```go
// Ignore close error - we're done with the file anyway
_ = file.Close()
```

## Documentation Standards

### Code Comments

- **Package comments** - Brief description of package purpose
- **Exported functions** - Document behavior, parameters, return values
- **Complex logic** - Explain the "why", not the "what"
- **Avoid obvious comments** - Don't comment self-explanatory code

### When to Update Docs

Update documentation when:

- Adding new features or commands
- Changing existing behavior
- Adding new configuration options
- Modifying the database schema

## Quick Reference

### Essential Commands

```bash
make build        # Build binaries
make test         # Run tests
make lint         # Run linter (requires golangci-lint)
go fmt ./...      # Format code
```

### Files to Know

| File | Purpose |
|------|---------|
| `AGENTS.md` | AI agent guide (architecture, schema) |
| `DEVELOPMENT.md` | Development practices (this file) |
| `.golangci.yml` | Linter configuration |
| `Makefile` | Build and deployment commands |

### Getting Help

- Check existing code for patterns
- Run `make help` for available commands
- Review recent commits for style examples
