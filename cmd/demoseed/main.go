// demoseed creates a demo database with sample data for screencasts.
// Usage: go run ./cmd/demoseed [output.db]
// Default output: ~/.local/share/task/demo.db
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func main() {
	// Determine output path
	var dbPath string
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	} else {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".local", "share", "task", "demo.db")
	}

	// Remove existing demo database if it exists
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Remove(dbPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing existing db: %v\n", err)
			os.Exit(1)
		}
	}

	// Open/create the database
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	fmt.Printf("Creating demo database at: %s\n", dbPath)

	// Create sample projects
	projects := []struct {
		Name         string
		Path         string
		Instructions string
		Color        string
	}{
		{
			Name:         "acme-webapp",
			Path:         "/tmp/demo/acme-webapp",
			Instructions: "This is the main Acme Corp web application. Built with React + TypeScript frontend and Go backend. Follow the existing patterns for API endpoints and use the shared component library.",
			Color:        "#61AFEF",
		},
		{
			Name:         "mobile-app",
			Path:         "/tmp/demo/mobile-app",
			Instructions: "React Native mobile app for iOS and Android. Uses Expo for development. Test changes on both platforms before submitting.",
			Color:        "#98C379",
		},
		{
			Name:         "infra",
			Path:         "/tmp/demo/infra",
			Instructions: "Infrastructure-as-code repository using Terraform and Kubernetes. Always run `terraform plan` before applying changes. Use staging environment for testing.",
			Color:        "#E5C07B",
		},
		{
			Name:         "personal",
			Path:         "/tmp/demo/personal",
			Instructions: "Personal tasks and notes.",
			Color:        "#C678DD",
		},
	}

	for _, p := range projects {
		// Create dummy project directory
		os.MkdirAll(p.Path, 0755)

		_, err := database.Exec(`
			INSERT OR REPLACE INTO projects (name, path, instructions, color)
			VALUES (?, ?, ?, ?)
		`, p.Name, p.Path, p.Instructions, p.Color)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating project %s: %v\n", p.Name, err)
			os.Exit(1)
		}
		fmt.Printf("  Created project: %s\n", p.Name)
	}

	// Create sample tasks with realistic scenarios
	now := time.Now()
	tasks := []struct {
		Title       string
		Body        string
		Status      string
		Type        string
		Project     string
		CreatedAt   time.Time
		StartedAt   *time.Time
		CompletedAt *time.Time
		Tags        string
	}{
		// Done tasks
		{
			Title:       "Add dark mode toggle to settings page",
			Body:        "Users have requested a dark mode option. Add a toggle in the settings page that persists the preference to localStorage and applies the appropriate CSS variables.",
			Status:      db.StatusDone,
			Type:        db.TypeCode,
			Project:     "acme-webapp",
			CreatedAt:   now.Add(-72 * time.Hour),
			StartedAt:   ptr(now.Add(-70 * time.Hour)),
			CompletedAt: ptr(now.Add(-68 * time.Hour)),
			Tags:        "ui,feature,settings",
		},
		{
			Title:       "Fix login redirect loop on Safari",
			Body:        "Users on Safari are experiencing an infinite redirect loop after login. Investigate the cookie handling and fix the issue.",
			Status:      db.StatusDone,
			Type:        db.TypeCode,
			Project:     "acme-webapp",
			CreatedAt:   now.Add(-48 * time.Hour),
			StartedAt:   ptr(now.Add(-47 * time.Hour)),
			CompletedAt: ptr(now.Add(-46 * time.Hour)),
			Tags:        "bug,auth,safari",
		},
		{
			Title:       "Write Q4 product roadmap",
			Body:        "Create the product roadmap document for Q4 2024. Include: feature priorities, timeline estimates, and resource allocation. Reference the customer feedback survey results.",
			Status:      db.StatusDone,
			Type:        db.TypeWriting,
			Project:     "personal",
			CreatedAt:   now.Add(-96 * time.Hour),
			StartedAt:   ptr(now.Add(-94 * time.Hour)),
			CompletedAt: ptr(now.Add(-90 * time.Hour)),
			Tags:        "planning,roadmap",
		},

		// Processing task
		{
			Title:     "Implement user activity dashboard",
			Body:      "Create a new dashboard showing user activity metrics including:\n- Daily/weekly/monthly active users\n- Feature usage statistics\n- User retention charts\n\nUse the existing analytics API and create new React components in the dashboard module.",
			Status:    db.StatusProcessing,
			Type:      db.TypeCode,
			Project:   "acme-webapp",
			CreatedAt: now.Add(-2 * time.Hour),
			StartedAt: ptr(now.Add(-1 * time.Hour)),
			Tags:      "feature,dashboard,analytics",
		},

		// Blocked task
		{
			Title:     "Upgrade Kubernetes cluster to 1.28",
			Body:      "Upgrade the production Kubernetes cluster from 1.26 to 1.28. Review breaking changes and update any deprecated APIs. Coordinate with the team for maintenance window.",
			Status:    db.StatusBlocked,
			Type:      db.TypeCode,
			Project:   "infra",
			CreatedAt: now.Add(-24 * time.Hour),
			StartedAt: ptr(now.Add(-20 * time.Hour)),
			Tags:      "kubernetes,upgrade,infrastructure",
		},

		// Queued tasks
		{
			Title:     "Add push notification support",
			Body:      "Implement push notifications for the mobile app using Firebase Cloud Messaging. Support both iOS and Android. Allow users to configure notification preferences.",
			Status:    db.StatusQueued,
			Type:      db.TypeCode,
			Project:   "mobile-app",
			CreatedAt: now.Add(-6 * time.Hour),
			Tags:      "feature,notifications,mobile",
		},

		// Backlog tasks
		{
			Title:     "Refactor authentication middleware",
			Body:      "The current auth middleware is becoming complex. Refactor it to use a cleaner pattern with proper separation of concerns. Consider using middleware chains.",
			Status:    db.StatusBacklog,
			Type:      db.TypeCode,
			Project:   "acme-webapp",
			CreatedAt: now.Add(-120 * time.Hour),
			Tags:      "refactor,auth,technical-debt",
		},
		{
			Title:     "Add E2E tests for checkout flow",
			Body:      "Create comprehensive end-to-end tests for the entire checkout flow including cart, payment, and order confirmation. Use Playwright for browser automation.",
			Status:    db.StatusBacklog,
			Type:      db.TypeCode,
			Project:   "acme-webapp",
			CreatedAt: now.Add(-96 * time.Hour),
			Tags:      "testing,e2e,checkout",
		},
		{
			Title:     "Design API rate limiting strategy",
			Body:      "We need to implement rate limiting for our public API. Research best practices and propose a strategy considering:\n- Rate limits per endpoint\n- User tier-based limits\n- Response headers\n- Error handling",
			Status:    db.StatusBacklog,
			Type:      db.TypeThinking,
			Project:   "acme-webapp",
			CreatedAt: now.Add(-72 * time.Hour),
			Tags:      "api,security,architecture",
		},
		{
			Title:     "Investigate slow search performance",
			Body:      "Users are reporting slow search results (>3s response time). Profile the search API and database queries. Identify optimization opportunities.",
			Status:    db.StatusBacklog,
			Type:      db.TypeCode,
			Project:   "acme-webapp",
			CreatedAt: now.Add(-48 * time.Hour),
			Tags:      "performance,search,investigation",
		},
		{
			Title:     "Create onboarding tutorial for mobile app",
			Body:      "Design and implement an onboarding flow for new users. Include feature highlights, permission requests, and account setup.",
			Status:    db.StatusBacklog,
			Type:      db.TypeCode,
			Project:   "mobile-app",
			CreatedAt: now.Add(-24 * time.Hour),
			Tags:      "onboarding,ux,mobile",
		},
		{
			Title:     "Set up automated database backups",
			Body:      "Configure automated daily backups for production databases. Store backups in S3 with 30-day retention. Include backup verification.",
			Status:    db.StatusBacklog,
			Type:      db.TypeCode,
			Project:   "infra",
			CreatedAt: now.Add(-168 * time.Hour),
			Tags:      "backup,database,automation",
		},
	}

	for _, t := range tasks {
		var startedAt, completedAt interface{}
		if t.StartedAt != nil {
			startedAt = *t.StartedAt
		}
		if t.CompletedAt != nil {
			completedAt = *t.CompletedAt
		}

		_, err := database.Exec(`
			INSERT INTO tasks (title, body, status, type, project, created_at, started_at, completed_at, tags)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, t.Title, t.Body, t.Status, t.Type, t.Project, t.CreatedAt, startedAt, completedAt, t.Tags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating task %s: %v\n", t.Title, err)
			os.Exit(1)
		}
		fmt.Printf("  Created task: [%s] %s\n", t.Status, truncate(t.Title, 50))
	}

	// Create sample memories
	memories := []struct {
		Project  string
		Category string
		Content  string
	}{
		// acme-webapp memories
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryPattern,
			Content:  "Use React Query for all data fetching. Mutations should invalidate relevant queries.",
		},
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryPattern,
			Content:  "API endpoints follow RESTful conventions with versioning (e.g., /api/v1/users).",
		},
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryContext,
			Content:  "The app uses Tailwind CSS for styling. Custom colors are defined in tailwind.config.js.",
		},
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryDecision,
			Content:  "Chose Zustand over Redux for state management due to simpler boilerplate and better TypeScript support.",
		},
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryGotcha,
			Content:  "The authentication token refresh happens silently - don't store tokens in localStorage, use httpOnly cookies.",
		},
		{
			Project:  "acme-webapp",
			Category: db.MemoryCategoryGotcha,
			Content:  "Safari has stricter cookie policies. Always test auth flows in Safari before deploying.",
		},

		// mobile-app memories
		{
			Project:  "mobile-app",
			Category: db.MemoryCategoryPattern,
			Content:  "Use expo-router for navigation. All screens are in the app/ directory following file-based routing.",
		},
		{
			Project:  "mobile-app",
			Category: db.MemoryCategoryContext,
			Content:  "The app targets iOS 14+ and Android 10+. Use platform-specific code sparingly.",
		},
		{
			Project:  "mobile-app",
			Category: db.MemoryCategoryDecision,
			Content:  "Using react-native-reanimated for animations instead of Animated API for better performance.",
		},

		// infra memories
		{
			Project:  "infra",
			Category: db.MemoryCategoryPattern,
			Content:  "All Terraform changes must go through PR review. Use terraform plan output in PR descriptions.",
		},
		{
			Project:  "infra",
			Category: db.MemoryCategoryGotcha,
			Content:  "The staging cluster uses a smaller node pool. Don't copy production HPA settings directly.",
		},
		{
			Project:  "infra",
			Category: db.MemoryCategoryContext,
			Content:  "AWS us-east-1 is primary region, us-west-2 is DR. All critical services are multi-region.",
		},
	}

	for _, m := range memories {
		_, err := database.Exec(`
			INSERT INTO project_memories (project, category, content)
			VALUES (?, ?, ?)
		`, m.Project, m.Category, m.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating memory: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("  Created %d project memories\n", len(memories))

	// Add some sample task logs for the processing task
	_, err = database.Exec(`
		INSERT INTO task_logs (task_id, line_type, content)
		SELECT id, 'system', 'Starting task execution...'
		FROM tasks WHERE status = 'processing' LIMIT 1
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not create sample logs: %v\n", err)
	}

	_, err = database.Exec(`
		INSERT INTO task_logs (task_id, line_type, content)
		SELECT id, 'tool', 'Read: src/components/Dashboard.tsx'
		FROM tasks WHERE status = 'processing' LIMIT 1
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not create sample logs: %v\n", err)
	}

	_, err = database.Exec(`
		INSERT INTO task_logs (task_id, line_type, content)
		SELECT id, 'output', 'Analyzing existing dashboard structure...'
		FROM tasks WHERE status = 'processing' LIMIT 1
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not create sample logs: %v\n", err)
	}

	fmt.Printf("  Created sample task logs\n")

	fmt.Println()
	fmt.Println("Demo database created successfully!")
	fmt.Println()
	fmt.Println("To use the demo database, run:")
	fmt.Printf("  WORKTREE_DB_PATH=%s ./bin/task -l\n", dbPath)
	fmt.Println()
	fmt.Println("Or add to your shell session:")
	fmt.Printf("  export WORKTREE_DB_PATH=%s\n", dbPath)
}

func ptr(t time.Time) *time.Time {
	return &t
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
