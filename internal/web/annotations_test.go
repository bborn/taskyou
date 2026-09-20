package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// 1x1 transparent PNG
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func annotationBody(screenshot bool) string {
	b := map[string]interface{}{
		"url":         "http://localhost:3142/products",
		"title":       "Products",
		"instruction": "fix the button",
		"viewport":    map[string]interface{}{"width": 1440, "height": 900, "dpr": 2},
		"annotations": []map[string]interface{}{
			{"kind": "element", "label": 1, "selector": "#save-btn", "tag": "button",
				"text": "Save", "html": "<button id=\"save-btn\">Save</button>",
				"rect":    map[string]float64{"x": 1, "y": 2, "w": 3, "h": 4},
				"styles":  map[string]string{"color": "rgb(0, 0, 0)"},
				"comment": "make this primary"},
			{"kind": "note", "label": 2, "comment": "general note"},
		},
	}
	if screenshot {
		b["screenshot"] = "data:image/png;base64," + tinyPNG
	}
	out, _ := json.Marshal(b)
	return string(out)
}

// setupAnnotationTask makes a task whose agent is waiting on the user — the
// state a browser annotation is normally sent in. withPane tags a pane as that
// task's agent on the fake tmux, which is the only thing the nudge will accept
// as a delivery target.
func setupAnnotationTask(t *testing.T, database *db.DB, runner *mockRunner, withPane bool) (*db.Task, string) {
	t.Helper()
	wt := t.TempDir()
	task := &db.Task{Title: "Anno task", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = wt
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if withPane {
		database.UpdateTaskPaneIDs(task.ID, "%9", "")
		tagPaneInFakeTmux(runner, task.ID, "%9")
	}
	return task, wt
}

// tagPaneInFakeTmux adds a pane to the fake `tmux list-panes -a` answer.
func tagPaneInFakeTmux(runner *mockRunner, taskID int64, pane string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.outputByCmd == nil {
		runner.outputByCmd = map[string][]byte{}
	}
	line := fmt.Sprintf("%s %d agent\n", pane, taskID)
	runner.outputByCmd["list-panes"] = append(runner.outputByCmd["list-panes"], line...)
}

// prompts returns the recorded calls with the pane lookups dropped, so a test
// can talk about deliveries rather than about tmux bookkeeping.
func prompts(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if len(c) > 1 && c[1] == "list-panes" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// setupAnnotationServer is setupServer with a coalesce window short enough to
// keep tests fast, but long enough that two back-to-back posts still merge.
func setupAnnotationServer(t *testing.T) (*Server, *db.DB, *mockRunner) {
	t.Helper()
	srv, database, runner := setupServer(t)
	srv.annoWindow = 80 * time.Millisecond
	return srv, database, runner
}

func postAnnotations(t *testing.T, srv *Server, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/tasks/%d/annotations", id), strings.NewReader(body))
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	w := httptest.NewRecorder()
	srv.handleTaskAnnotations(w, req)
	return w
}

func TestHandleAnnotations_WritesBundleAndNudges(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, runner, true)

	w := postAnnotations(t, srv, task.ID, annotationBody(true))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OK     bool   `json:"ok"`
		Path   string `json:"path"`
		Nudged bool   `json:"nudged"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.OK || !resp.Nudged {
		t.Fatalf("resp = %+v, want ok+nudged", resp)
	}

	// Bundle on disk
	mdPath := filepath.Join(wt, resp.Path)
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read annotation.md: %v", err)
	}
	for _, want := range []string{"http://localhost:3142/products", "make this primary", "#save-btn", "screenshot.png", "fix the button"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("annotation.md missing %q", want)
		}
	}
	png, err := os.ReadFile(filepath.Join(filepath.Dir(mdPath), "screenshot.png"))
	if err != nil {
		t.Fatalf("read screenshot.png: %v", err)
	}
	wantPNG, _ := base64.StdEncoding.DecodeString(tinyPNG)
	if string(png) != string(wantPNG) {
		t.Error("screenshot.png content mismatch")
	}

	// .gitignore guard
	gi, err := os.ReadFile(filepath.Join(wt, ".taskyou", "annotations", ".gitignore"))
	if err != nil || strings.TrimSpace(string(gi)) != "*" {
		t.Errorf("gitignore = %q err=%v, want *", gi, err)
	}

	// One pasted prompt then Enter, into the tagged pane, once the coalesce
	// window closes.
	sent := runner.waitForPrompts(t, 3)
	if len(sent) != 3 {
		t.Fatalf("expected set-buffer, paste-buffer, Enter; got %v", sent)
	}
	text := sent[0]
	if text[1] != "set-buffer" {
		t.Errorf("first call = %v, want set-buffer", text)
	}
	nudge := text[len(text)-1]
	if !strings.Contains(nudge, resp.Path) {
		t.Errorf("nudge %q missing bundle path %q", nudge, resp.Path)
	}
	if strings.ContainsAny(nudge, "\n") {
		t.Error("nudge must be single-line")
	}
	if sent[1][1] != "paste-buffer" || sent[1][len(sent[1])-1] != "%9" {
		t.Errorf("paste call = %v, want a paste into the tagged pane %%9", sent[1])
	}
	if fmt.Sprint(sent[2]) != fmt.Sprint([]string{"tmux", "send-keys", "-t", "%9", "Enter"}) {
		t.Errorf("last call = %v, want bare Enter", sent[2])
	}
}

// The pane id on the row is not evidence of anything: tmux reuses ids. Only a
// tagged pane is a delivery target.
func TestAnnotationNudge_IgnoresAStaleStoredPane(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, _ := setupAnnotationTask(t, database, runner, false)
	// The row still names %9 — which now belongs to another task's agent.
	database.UpdateTaskPaneIDs(task.ID, "%9", "")
	tagPaneInFakeTmux(runner, task.ID+1000, "%9")
	tagPaneInFakeTmux(runner, task.ID, "%77")

	postAnnotations(t, srv, task.ID, annotationBody(false))

	sent := runner.waitForPrompts(t, 3)
	for _, call := range sent {
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) && call[i+1] != "%77" {
				t.Fatalf("nudge aimed at %q, want the tagged pane %%77: %v", call[i+1], call)
			}
		}
	}
}

// A working agent is not typed over. The bundle is on disk, so the nudge waits
// for the agent to stop rather than interrupting it or giving up.
func TestAnnotationNudge_WaitsForABusyAgent(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	srv.nudgeWindow, srv.nudgeRetry = 5*time.Second, 10*time.Millisecond
	task, _ := setupAnnotationTask(t, database, runner, true)
	database.SetTaskStatus(task.ID, db.StatusProcessing, db.ActorSystem, "agent is mid-turn", db.Evidence{Observed: "test fixture"})

	postAnnotations(t, srv, task.ID, annotationBody(false))

	// Nothing is delivered while it works.
	time.Sleep(200 * time.Millisecond)
	if sent := prompts(runner.snapshot()); len(sent) != 0 {
		t.Fatalf("nudge typed into a working agent: %v", sent)
	}

	// It stops; the nudge goes out.
	database.SetTaskStatus(task.ID, db.StatusBlocked, db.ActorSystem, "agent finished its turn", db.Evidence{Observed: "test fixture"})
	sent := runner.waitForPrompts(t, 3)
	if len(sent) < 3 || sent[0][1] != "set-buffer" {
		t.Fatalf("nudge not delivered once the agent stopped: %v", sent)
	}
}

// An agent that never stops does not hold the nudge forever, and the task log
// says where the annotations went.
func TestAnnotationNudge_GivesUpOnAnAgentThatNeverStops(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	srv.nudgeWindow, srv.nudgeRetry = 30*time.Millisecond, 5*time.Millisecond
	task, _ := setupAnnotationTask(t, database, runner, true)
	database.SetTaskStatus(task.ID, db.StatusProcessing, db.ActorSystem, "agent is mid-turn", db.Evidence{Observed: "test fixture"})

	postAnnotations(t, srv, task.ID, annotationBody(false))

	deadline := time.Now().Add(3 * time.Second)
	for {
		logs, _ := database.GetTaskLogs(task.ID, 20)
		for _, l := range logs {
			if strings.Contains(l.Content, "never stopped long enough") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log line explaining the undelivered nudge; logs = %v", logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleAnnotations_NoPane_StillWrites(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, runner, false)

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Nudged bool   `json:"nudged"`
		Path   string `json:"path"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Nudged {
		t.Error("nudged = true, want false")
	}
	if _, err := os.Stat(filepath.Join(wt, resp.Path)); err != nil {
		t.Errorf("bundle not written: %v", err)
	}
	// No tagged pane, so the flush asks tmux and then types nothing.
	time.Sleep(200 * time.Millisecond)
	if got := prompts(runner.snapshot()); len(got) != 0 {
		t.Errorf("expected nothing typed, got %v", got)
	}
	md, _ := os.ReadFile(filepath.Join(wt, resp.Path))
	if strings.Contains(string(md), "screenshot.png") {
		t.Error("annotation.md should not reference missing screenshot")
	}
}

func TestHandleAnnotations_FallsBackToProjectPath(t *testing.T) {
	srv, database, _ := setupServer(t)
	projDir := t.TempDir()
	database.CreateProject(&db.Project{Name: "annoproj", Path: projDir})
	task := &db.Task{Title: "No worktree", Status: db.StatusProcessing, Project: "annoproj"}
	database.CreateTask(task)

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Path string `json:"path"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if _, err := os.Stat(filepath.Join(projDir, resp.Path)); err != nil {
		t.Errorf("bundle not in project dir: %v", err)
	}
}

func TestHandleAnnotations_MissingRootDir(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := &db.Task{Title: "Rootless", Status: db.StatusProcessing, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = "/nonexistent/ty-chrome-test-root"
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("update task: %v", err)
	}

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAnnotations_EmptyAnnotations(t *testing.T) {
	srv, database, runner := setupServer(t)
	task, _ := setupAnnotationTask(t, database, runner, true)

	w := postAnnotations(t, srv, task.ID, `{"url":"http://x","annotations":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// Two submissions landing inside the coalesce window belong to one thought:
// one bundle, one prompt for the executor.
func TestHandleAnnotations_CoalescesRapidSubmissions(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, runner, true)

	first := postAnnotations(t, srv, task.ID, annotationBody(true))
	second := postAnnotations(t, srv, task.ID, annotationBody(true))

	var r1, r2 struct {
		Path string `json:"path"`
	}
	json.NewDecoder(first.Body).Decode(&r1)
	json.NewDecoder(second.Body).Decode(&r2)
	if r1.Path != r2.Path {
		t.Errorf("submissions went to different bundles: %q vs %q", r1.Path, r2.Path)
	}

	entries, _ := os.ReadDir(filepath.Join(wt, ".taskyou", "annotations"))
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Errorf("expected 1 bundle dir, got %d", dirs)
	}

	// Both screenshots kept, both submissions rendered.
	md, _ := os.ReadFile(filepath.Join(wt, r1.Path))
	for _, want := range []string{"2 submissions", "Submission 1", "Submission 2", "screenshot-2.png"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("annotation.md missing %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, filepath.Dir(r1.Path), "screenshot-2.png")); err != nil {
		t.Errorf("second screenshot not written: %v", err)
	}

	// Exactly one nudge, not two.
	calls := runner.waitForPrompts(t, 3)
	time.Sleep(150 * time.Millisecond)
	if got := prompts(runner.snapshot()); len(got) != 3 {
		t.Errorf("expected 1 nudge (3 tmux calls), got %d: %v", len(got), got)
	}
	if nudge := calls[0][len(calls[0])-1]; !strings.Contains(nudge, "2 submissions") {
		t.Errorf("nudge should say how many submissions: %q", nudge)
	}
}

// Submissions spaced beyond the window are separate thoughts: separate bundles,
// separate nudges.
func TestHandleAnnotations_SeparateBundlesWhenSpaced(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, _ := setupAnnotationTask(t, database, runner, true)

	postAnnotations(t, srv, task.ID, annotationBody(false))
	runner.waitForPrompts(t, 3)
	postAnnotations(t, srv, task.ID, annotationBody(false))
	calls := runner.waitForPrompts(t, 6)

	first, second := calls[0][len(calls[0])-1], calls[3][len(calls[3])-1]
	if first == second {
		t.Errorf("both nudges point at the same bundle: %q", first)
	}
}

// A prompt and its Enter must stay together. Two nudges racing for one agent
// used to interleave into a single garbled line — the text of the second landing
// between the first's text and its Enter.
func TestAnnotationNudges_ToOneAgentDoNotInterleave(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	// Without a per-call cost the calls are never preempted, and this would pass
	// even with the serialization removed.
	runner.mu.Lock()
	runner.delay = 5 * time.Millisecond
	runner.mu.Unlock()

	task, _ := setupAnnotationTask(t, database, runner, true)

	const nudges = 4
	var wg sync.WaitGroup
	for i := 0; i < nudges; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			srv.nudgeAgent(task.ID, fmt.Sprintf("[ty-chrome] bundle %d is ready to read", i), "path")
		}(i)
	}
	wg.Wait()

	calls := runner.waitForPrompts(t, nudges*3)
	if len(calls) != nudges*3 {
		t.Fatalf("expected %d delivery calls, got %d: %v", nudges*3, len(calls), calls)
	}
	for i := 0; i < len(calls); i += 3 {
		stage, paste, enter := calls[i], calls[i+1], calls[i+2]
		if stage[1] != "set-buffer" || paste[1] != "paste-buffer" || enter[1] != "send-keys" {
			t.Fatalf("nudges interleaved at call %d: %v", i, calls)
		}
		if argAfterFlag(stage, "-b") != argAfterFlag(paste, "-b") {
			t.Fatalf("a nudge pasted another nudge's text: %v then %v", stage, paste)
		}
		if paste[len(paste)-1] != enter[3] {
			t.Errorf("paste/Enter pair split across panes: %v then %v", paste, enter)
		}
	}
}

// Nudges for different tasks have no reason to wait for each other; each still
// arrives whole, in its own pane.
func TestAnnotationNudges_ToDifferentAgentsEachArriveWhole(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	runner.mu.Lock()
	runner.delay = 5 * time.Millisecond
	runner.mu.Unlock()

	const tasks = 6
	ids := make([]int64, 0, tasks)
	panes := map[int64]string{}
	for i := 0; i < tasks; i++ {
		task, _ := setupAnnotationTask(t, database, runner, false)
		pane := fmt.Sprintf("%%%d", 100+i)
		tagPaneInFakeTmux(runner, task.ID, pane)
		panes[task.ID] = pane
		ids = append(ids, task.ID)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			postAnnotations(t, srv, id, annotationBody(false))
		}(id)
	}
	wg.Wait()

	calls := runner.waitForPrompts(t, tasks*3)

	// Per pane, the transcript must read paste, Enter, and nothing else.
	byPane := map[string][][]string{}
	for _, c := range calls {
		switch c[1] {
		case "paste-buffer":
			byPane[c[len(c)-1]] = append(byPane[c[len(c)-1]], c)
		case "send-keys":
			byPane[c[3]] = append(byPane[c[3]], c)
		}
	}
	for _, pane := range panes {
		got := byPane[pane]
		if len(got) != 2 || got[0][1] != "paste-buffer" || got[1][len(got[1])-1] != "Enter" {
			t.Errorf("pane %s received %v, want one paste followed by Enter", pane, got)
		}
	}
}

func argAfterFlag(call []string, flag string) string {
	for i, a := range call {
		if a == flag && i+1 < len(call) {
			return call[i+1]
		}
	}
	return ""
}

// A bundle written here for a task running on another host is a bundle nobody
// reads: the coordinator has a checkout of the same project at a very similar
// path (and a moved task's old worktree at exactly the same one), so the write
// succeeds, reports a path, and the agent on the host never sees it.
func TestHandleAnnotations_RefusesToStageForATaskOnAnotherHost(t *testing.T) {
	srv, database, runner := setupAnnotationServer(t)
	task, wt := setupAnnotationTask(t, database, runner, true)
	if err := database.SetTaskPlacement(task.ID, "ol-agents", "placement plugin"); err != nil {
		t.Fatal(err)
	}

	w := postAnnotations(t, srv, task.ID, annotationBody(true))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ol-agents") {
		t.Errorf("the refusal does not say where the task runs: %s", w.Body.String())
	}
	if entries, err := os.ReadDir(filepath.Join(wt, ".taskyou")); err == nil && len(entries) > 0 {
		t.Errorf("wrote into this machine's worktree anyway: %v", entries)
	}
}

// The same rule for the browser bridge's screenshots and DOM snapshots: nothing
// is written to this machine for a task whose worktree is on another one.
func TestBrowserArtefactsAreNotWrittenForATaskOnAnotherHost(t *testing.T) {
	srv, database, _ := setupServer(t)
	wt := t.TempDir()
	task := &db.Task{Title: "placed", Status: db.StatusBlocked, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	task.WorktreePath = wt
	if err := database.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	task.PlacementTarget = "ol-agents"
	if root := srv.resolveTaskRoot(task); root != "" {
		t.Errorf("browser artefacts would be written to %q, on the wrong machine", root)
	}

	// And the payload is dropped rather than handed back: a screenshot is up to
	// 20 MB of base64 and this result is the agent's tool output.
	shot := json.RawMessage(`{"ok":true,"data":"data:image/png;base64,` + tinyPNG + `"}`)
	result, ok := srv.materializeBrowserResult(task, "screenshot", shot).(map[string]interface{})
	if !ok {
		t.Fatalf("screenshot result is not an object: %#v", result)
	}
	if _, inlined := result["data"]; inlined {
		t.Error("a placed task's screenshot was inlined into the agent's output")
	}
	if problem, _ := result["error"].(string); !strings.Contains(problem, "ol-agents") {
		t.Errorf("the result does not say why the screenshot is missing: %#v", result)
	}
	// An action that carries nothing bulky still works: the relay does not care
	// which machine the agent is on.
	clicked, _ := srv.materializeBrowserResult(task, "click", json.RawMessage(`{"ok":true,"clicked":"#save"}`)).(map[string]interface{})
	if clicked["clicked"] != "#save" {
		t.Errorf("a plain browser action was altered for a placed task: %#v", clicked)
	}

	task.PlacementTarget = ""
	if root := srv.resolveTaskRoot(task); root != wt {
		t.Errorf("a local task lost its worktree root: %q", root)
	}
}
