package web

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/events"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/notify"
	"github.com/bborn/workflow/internal/routine"
)

type routineJSON struct {
	Name           string         `json:"name"`
	Project        string         `json:"project,omitempty"`
	Model          string         `json:"model"`
	PermissionMode string         `json:"permission_mode"`
	Timeout        string         `json:"timeout"`
	Disabled       bool           `json:"disabled"`
	Schedule       *scheduleJSON  `json:"schedule,omitempty"`
	LastRun        *db.RoutineRun `json:"last_run,omitempty"`
}

type scheduleJSON struct {
	Backend string `json:"backend"`
	Detail  string `json:"detail"`
}

// handleListRoutines returns the routine fleet: definitions, schedules, and
// latest run outcomes — the same picture as the TUI's routines view.
func (s *Server) handleListRoutines(w http.ResponseWriter, r *http.Request) {
	routines, err := routine.List()
	if err != nil {
		jsonErr(w, "failed to list routines: "+err.Error(), http.StatusInternalServerError)
		return
	}

	names := make([]string, len(routines))
	for i, rt := range routines {
		names[i] = rt.Name
	}
	schedules, err := routine.LoadSchedules(names)
	if err != nil {
		schedules = nil // schedules are supplementary; don't fail the list
	}
	latest, err := s.db.LatestRoutineRuns()
	if err != nil {
		latest = nil
	}

	result := make([]routineJSON, 0, len(routines))
	for _, rt := range routines {
		entry := routineJSON{
			Name:           rt.Name,
			Project:        rt.Project,
			Model:          rt.Model,
			PermissionMode: rt.PermissionMode,
			Timeout:        rt.Timeout.String(),
			Disabled:       rt.Disabled,
			LastRun:        latest[rt.Name],
		}
		if sched := schedules[rt.Name]; sched != nil {
			entry.Schedule = &scheduleJSON{Backend: sched.Backend, Detail: sched.Detail}
		}
		result = append(result, entry)
	}
	jsonOK(w, result)
}

func (s *Server) requireRoutine(w http.ResponseWriter, r *http.Request) (*routine.Routine, bool) {
	name := r.PathValue("name")
	if err := routine.ValidateName(name); err != nil {
		jsonErr(w, "invalid routine name", http.StatusBadRequest)
		return nil, false
	}
	if !routine.Exists(name) {
		jsonErr(w, "routine not found", http.StatusNotFound)
		return nil, false
	}
	rt, err := routine.Load(name)
	if err != nil {
		jsonErr(w, "failed to load routine: "+err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return rt, true
}

func (s *Server) handleListRoutineRuns(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.requireRoutine(w, r)
	if !ok {
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.db.ListRoutineRuns(rt.Name, limit)
	if err != nil {
		jsonErr(w, "failed to list runs", http.StatusInternalServerError)
		return
	}
	if runs == nil {
		runs = []*db.RoutineRun{}
	}
	jsonOK(w, runs)
}

// handleRoutineRunLog streams the full log file of one run. The path comes
// from the run row (written by the runner), never from the request.
func (s *Server) handleRoutineRunLog(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.requireRoutine(w, r)
	if !ok {
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("run"), 10, 64)
	if err != nil {
		jsonErr(w, "invalid run id", http.StatusBadRequest)
		return
	}
	run, err := s.db.GetRoutineRun(runID)
	if err != nil || run == nil || run.Routine != rt.Name {
		jsonErr(w, "run not found", http.StatusNotFound)
		return
	}
	if run.LogPath == "" {
		jsonErr(w, "run has no log", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(run.LogPath)
	if err != nil {
		// Log files are pruned independently of run rows; fall back to the
		// stored output tail.
		jsonOK(w, map[string]string{"log": run.Output, "note": "full log unavailable; showing stored tail"})
		return
	}
	jsonOK(w, map[string]string{"log": string(data)})
}

// handleRunRoutine triggers a routine run asynchronously. The run row appears
// immediately in "running" state; clients poll the list for the outcome.
func (s *Server) handleRunRoutine(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.requireRoutine(w, r)
	if !ok {
		return
	}
	if rt.Disabled {
		jsonErr(w, "routine is disabled", http.StatusConflict)
		return
	}
	if latest, err := s.db.LatestRoutineRuns(); err == nil {
		if run := latest[rt.Name]; run != nil && run.Status == db.RoutineRunStatusRunning {
			jsonErr(w, "routine is already running", http.StatusConflict)
			return
		}
	}

	emitter := events.New(hooks.DefaultHooksDir())
	emitter.SetNotifier(notify.New(s.db))
	runner := &routine.Runner{
		DB:      s.db,
		Emitter: emitter,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rt.Timeout+time.Minute)
		defer cancel()
		// Run records state, logs, and failure alerting itself.
		_, _ = runner.Run(ctx, rt)
	}()

	w.WriteHeader(http.StatusAccepted)
	jsonOK(w, map[string]any{"started": true, "routine": rt.Name})
}
