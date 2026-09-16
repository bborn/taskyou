package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/reaper"
)

// reapSweepPolicy builds the sweep policy for `ty sessions cleanup` from the
// configured thresholds, falling back to the reaper's defaults.
func reapSweepPolicy(database *db.DB, procs []reaper.Process) reaper.Policy {
	return reaper.Policy{
		Now:                  time.Now(),
		DoneGrace:            reaper.DefaultDoneGrace,
		BlockedIdle:          durationSetting(database, config.SettingReapBlockedIdle, reaper.DefaultBlockedIdle),
		OrphanMinAge:         durationSetting(database, config.SettingReapOrphanMinAge, reaper.DefaultOrphanMinAge),
		ReapOrphanDevServers: boolSetting(database, config.SettingReapOrphanDevServers, true),
		Protected:            reaper.ProtectedPIDs(procs),
	}
}

// reapExplicitPolicy builds the policy for a teardown the user asked for by
// name (`ty sessions suspend`). Staleness thresholds don't apply — the point of
// suspend is to free the memory now — and the sweep is scoped to exactly the
// tasks that were suspended. The agent's session ID stays in the database, so
// `ty retry` can still resume the conversation.
func reapExplicitPolicy(taskIDs map[int]bool, procs []reaper.Process) reaper.Policy {
	return reaper.Policy{
		Now:          time.Now(),
		DoneGrace:    0,
		BlockedIdle:  0,
		OrphanMinAge: reaper.DefaultOrphanMinAge,
		Protected:    reaper.ProtectedPIDs(procs),
		OnlyTasks:    taskIDs,
		Explicit:     true,
	}
}

// durationSetting reads a Go duration string from settings. "0" or "disabled"
// yields a threshold so large nothing ever trips it, which is how a user turns
// staleness-based reaping off without a separate flag.
func durationSetting(database *db.DB, key string, fallback time.Duration) time.Duration {
	if database == nil {
		return fallback
	}
	val, err := database.GetSetting(key)
	if err != nil || val == "" {
		return fallback
	}
	if val == "0" || val == "disabled" {
		return reaper.Never
	}
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d
	}
	return fallback
}

func boolSetting(database *db.DB, key string, fallback bool) bool {
	if database == nil {
		return fallback
	}
	val, err := database.GetSetting(key)
	if err != nil || val == "" {
		return fallback
	}
	switch val {
	case "false", "0", "no", "off", "disabled":
		return false
	case "true", "1", "yes", "on", "enabled":
		return true
	}
	return fallback
}

// loadTaskStates returns the status and last-activity time of every task,
// including closed ones. Absence from this map means the task is gone, which is
// itself a reap reason, so the query must not silently truncate — hence the
// explicit large limit (ListTasks maps 0 to a default of 100).
func loadTaskStates(database *db.DB) map[int]reaper.TaskState {
	states := map[int]reaper.TaskState{}
	if database == nil {
		return states
	}
	// IncludeTrashed: a trashed task still owns its worktree until the trash
	// sweep hard-deletes it, and its processes are as dead as a done task's.
	// Leaving it out would make it look absent, and absent is never reaped.
	tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true, IncludeTrashed: true, Limit: -1})
	if err != nil {
		return states
	}
	for _, task := range tasks {
		dir := ""
		if task.WorktreePath != "" {
			dir = filepath.Base(task.WorktreePath)
		}
		states[int(task.ID)] = reaper.TaskState{
			Exists:       true,
			Status:       task.Status,
			LastActivity: taskLastActivity(task),
			WorktreeDir:  dir,
		}
	}
	if trashed, err := database.ListTrashedTasks(); err == nil {
		for _, t := range trashed {
			state, ok := states[int(t.ID)]
			if !ok {
				continue
			}
			state.Status = "trashed"
			if t.DeletedAt.After(state.LastActivity) {
				state.LastActivity = t.DeletedAt
			}
			states[int(t.ID)] = state
		}
	}
	// Log lines are the finest-grained proof of life we have: an agent working
	// on a task writes them long before any status column changes.
	rows, err := database.Query(`SELECT task_id, MAX(created_at) FROM task_logs GROUP BY task_id`)
	if err != nil {
		return states
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int
		var last db.LocalTime
		if err := rows.Scan(&taskID, &last); err != nil {
			continue
		}
		state, ok := states[taskID]
		if !ok {
			continue
		}
		if last.Time.After(state.LastActivity) {
			state.LastActivity = last.Time
			states[taskID] = state
		}
	}
	return states
}

// taskLastActivity is the most recent sign of life on the task row itself.
func taskLastActivity(task *db.Task) time.Time {
	latest := task.UpdatedAt.Time
	for _, t := range []*db.LocalTime{task.LastAccessedAt, task.CompletedAt, task.StartedAt} {
		if t != nil && t.Time.After(latest) {
			latest = t.Time
		}
	}
	if task.CreatedAt.Time.After(latest) {
		latest = task.CreatedAt.Time
	}
	return latest
}

// reapSideProcesses scans the process table, decides what is orphaned, reports
// the reasoning, and (unless dryRun) SIGTERMs then SIGKILLs the orphans.
// Returns the number of processes killed.
//
// This is the half of cleanup that tmux cannot do. `tmux kill-window` only
// SIGHUPs the pane's foreground process group; a dev server that was
// backgrounded or disowned has left that group and, once its parent shell dies,
// is reparented to launchd where no signal from the window teardown can ever
// reach it. Matching on the worktree path finds it anyway.
func reapSideProcesses(database *db.DB, buildPolicy func([]reaper.Process) reaper.Policy, dryRun bool) int {
	procs, err := reaper.ScanProcesses()
	if err != nil {
		fmt.Fprintln(os.Stderr, dimStyle.Render("Warning: could not scan processes: "+err.Error()))
		return 0
	}

	decisions := reaper.Plan(procs, loadTaskStates(database), reaper.LivePanePIDs(), buildPolicy(procs))

	var toReap []reaper.Decision
	for _, d := range decisions {
		if d.Reap {
			toReap = append(toReap, d)
		}
	}

	if dryRun {
		if len(decisions) == 0 {
			fmt.Println(dimStyle.Render("No task-owned side processes found"))
			return 0
		}
		fmt.Printf("\n%s\n", boldStyle.Render(fmt.Sprintf("Side-process sweep (dry run) — %d considered, %d would be killed:", len(decisions), len(toReap))))
		for _, d := range decisions {
			marker := dimStyle.Render("  ·")
			if d.Reap {
				marker = errorStyle.Render("  ✗")
			}
			fmt.Printf("%s %s\n", marker, d.String())
		}
		return 0
	}

	if len(toReap) == 0 {
		return 0
	}

	fmt.Printf("\n%s\n", boldStyle.Render(fmt.Sprintf("Reaping %d orphaned side process(es):", len(toReap))))
	killed := 0
	for _, r := range reaper.Reap(toReap, reaper.DefaultTermGrace, nil) {
		if r.Err != nil {
			fmt.Printf("  %s %s: %v\n", errorStyle.Render("✗"), r.Decision.String(), r.Err)
			continue
		}
		fmt.Printf("  %s SIG%s %s\n", successStyle.Render("✓"), r.Signal, r.Decision.String())
		killed++
	}
	return killed
}
