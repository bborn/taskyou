# ty-chrome Browser Annotations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Chrome extension that annotates pages running on a task's dev-server port and delivers rich annotation bundles (markdown + screenshot) into the task's Claude executor pane, plus a side panel showing live executor output.

**Architecture:** Two halves. (1) Daemon: enrich task JSON with `port`/`worktree_path`/`has_executor`; add `POST /api/tasks/{id}/annotations` that writes `.taskyou/annotations/<ts>/annotation.md` + `screenshot.png` into the worktree and nudges the Claude pane via literal `tmux send-keys`. (2) Extension (MV3, vanilla JS, no build step): service worker owns daemon HTTP + tab→task port matching; on-demand content-script overlay captures element/region/note annotations; side panel shows matched task, live executor output (polling `/output`), and send controls.

**Tech Stack:** Go (net/http, existing `internal/web` patterns), Chrome MV3 (service worker, sidePanel, scripting, captureVisibleTab), vanilla JS/CSS.

**Note on granularity:** Go tasks are full TDD with complete code. Extension tasks specify complete interfaces, message protocols, and the non-obvious algorithms in code; the executing engineer writes the files to those contracts (no test framework exists for the extension — validation is the Task 6 end-to-end demo). Spec: `docs/superpowers/specs/2026-06-10-ty-chrome-annotations-design.md`.

---

### Task 1: Expose port / worktree_path / has_executor in task JSON

**Files:**
- Modify: `internal/web/handlers.go` (taskJSON struct + toTaskJSON, ~line 1012)
- Test: `internal/web/server_test.go`

- [ ] **Step 1: Write the failing test** (append to `server_test.go`)

```go
func TestTaskJSON_IncludesPortWorktreeExecutor(t *testing.T) {
	srv, database, _ := setupServer(t)

	task := &db.Task{Title: "Rich task", Status: db.StatusProcessing, Project: "personal"}
	database.CreateTask(task)
	task.Port = 3142
	task.WorktreePath = "/tmp/wt"
	database.UpdateTask(task)
	database.UpdateTaskPaneIDs(task.ID, "%7", "")

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.handleListTasks(w, req)

	var tasks []*taskJSON
	json.NewDecoder(w.Body).Decode(&tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Port != 3142 {
		t.Errorf("port = %d, want 3142", got.Port)
	}
	if got.WorktreePath != "/tmp/wt" {
		t.Errorf("worktree_path = %q, want /tmp/wt", got.WorktreePath)
	}
	if !got.HasExecutor {
		t.Error("has_executor = false, want true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestTaskJSON_IncludesPortWorktreeExecutor -v`
Expected: FAIL (compile error: unknown fields `Port`, `WorktreePath`, `HasExecutor`)

- [ ] **Step 3: Implement** — add to `taskJSON` struct:

```go
	Port         int    `json:"port,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	HasExecutor  bool   `json:"has_executor"`
```

and in `toTaskJSON`:

```go
	Port:         t.Port,
	WorktreePath: t.WorktreePath,
	HasExecutor:  t.ClaudePaneID != "",
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/web/ -v -run TestTaskJSON`
Expected: PASS (and `go test ./internal/web/` all green)

- [ ] **Step 5: Commit** — `feat(web): expose port, worktree_path, has_executor in task JSON`

---

### Task 2: Annotation intake endpoint

**Files:**
- Create: `internal/web/annotations.go`
- Modify: `internal/web/server.go` (route registration after `/input`, ~line 78)
- Test: `internal/web/annotations_test.go`

- [ ] **Step 1: Write the failing tests** (`internal/web/annotations_test.go`)

```go
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
	"testing"

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

func setupAnnotationTask(t *testing.T, database *db.DB, withPane bool) (*db.Task, string) {
	t.Helper()
	wt := t.TempDir()
	task := &db.Task{Title: "Anno task", Status: db.StatusProcessing, Project: "personal"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.WorktreePath = wt
	if err := database.UpdateTask(task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if withPane {
		database.UpdateTaskPaneIDs(task.ID, "%9", "")
	}
	return task, wt
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
	srv, database, runner := setupServer(t)
	task, wt := setupAnnotationTask(t, database, true)

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

	// Literal nudge then Enter
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 tmux calls, got %d: %v", len(runner.calls), runner.calls)
	}
	first := runner.calls[0]
	if first[0] != "tmux" || first[1] != "send-keys" || first[2] != "-t" || first[3] != "%9" || first[4] != "-l" {
		t.Errorf("first call = %v, want literal send-keys to %%9", first)
	}
	if !strings.Contains(first[5], resp.Path) {
		t.Errorf("nudge %q missing bundle path %q", first[5], resp.Path)
	}
	if strings.ContainsAny(first[5], "\n") {
		t.Error("nudge must be single-line")
	}
	second := runner.calls[1]
	if fmt.Sprint(second) != fmt.Sprint([]string{"tmux", "send-keys", "-t", "%9", "Enter"}) {
		t.Errorf("second call = %v, want bare Enter", second)
	}
}

func TestHandleAnnotations_NoPane_StillWrites(t *testing.T) {
	srv, database, runner := setupServer(t)
	task, wt := setupAnnotationTask(t, database, false)

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
	if len(runner.calls) != 0 {
		t.Errorf("expected no tmux calls, got %v", runner.calls)
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

func TestHandleAnnotations_NoRoot(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := &db.Task{Title: "Rootless", Status: db.StatusBacklog, Project: "nope-no-such"}
	database.CreateTask(task)

	w := postAnnotations(t, srv, task.ID, annotationBody(false))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleAnnotations_EmptyAnnotations(t *testing.T) {
	srv, database, _ := setupServer(t)
	task, _ := setupAnnotationTask(t, database, true)

	w := postAnnotations(t, srv, task.ID, `{"url":"http://x","annotations":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/web/ -run TestHandleAnnotations -v`
Expected: FAIL (compile error: `handleTaskAnnotations` undefined)

- [ ] **Step 3: Implement** `internal/web/annotations.go`:

```go
package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const annotationsMaxBody = 20 << 20 // 20 MB (screenshots)

type annotationRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type annotationItem struct {
	Kind     string            `json:"kind"` // element | region | note
	Label    int               `json:"label"`
	Selector string            `json:"selector,omitempty"`
	Tag      string            `json:"tag,omitempty"`
	Text     string            `json:"text,omitempty"`
	HTML     string            `json:"html,omitempty"`
	Rect     *annotationRect   `json:"rect,omitempty"`
	Styles   map[string]string `json:"styles,omitempty"`
	Comment  string            `json:"comment"`
}

type annotationsRequest struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Viewport struct {
		Width  int     `json:"width"`
		Height int     `json:"height"`
		DPR    float64 `json:"dpr"`
	} `json:"viewport"`
	Instruction string           `json:"instruction"`
	Annotations []annotationItem `json:"annotations"`
	Screenshot  string           `json:"screenshot"`
}

// handleTaskAnnotations receives a browser annotation bundle from ty-chrome,
// writes it into the task's worktree under .taskyou/annotations/<ts>/, and
// nudges the executor pane (if any) to read it.
func (s *Server) handleTaskAnnotations(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, annotationsMaxBody)
	var req annotationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Annotations) == 0 {
		jsonErr(w, "annotations required", http.StatusBadRequest)
		return
	}

	root := task.WorktreePath
	if root == "" && task.Project != "" {
		if p, _ := s.db.GetProjectByName(task.Project); p != nil {
			root = p.Path
		}
	}
	if root == "" {
		jsonErr(w, "task has no worktree or project path", http.StatusBadRequest)
		return
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		jsonErr(w, "task root directory does not exist", http.StatusBadRequest)
		return
	}

	annoRoot := filepath.Join(root, ".taskyou", "annotations")
	bundleName := fmt.Sprintf("%d", time.Now().UnixMilli())
	bundleDir := filepath.Join(annoRoot, bundleName)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		jsonErr(w, "failed to create annotation dir", http.StatusInternalServerError)
		return
	}
	// Keep bundles out of git status without touching the repo's .gitignore.
	gitignore := filepath.Join(annoRoot, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		os.WriteFile(gitignore, []byte("*\n"), 0o644)
	}

	hasScreenshot := false
	if req.Screenshot != "" {
		data := req.Screenshot
		if i := strings.Index(data, "base64,"); i >= 0 {
			data = data[i+len("base64,"):]
		}
		if png, err := base64.StdEncoding.DecodeString(data); err == nil && len(png) > 0 {
			if os.WriteFile(filepath.Join(bundleDir, "screenshot.png"), png, 0o644) == nil {
				hasScreenshot = true
			}
		}
	}

	md := buildAnnotationMarkdown(&req, hasScreenshot)
	if err := os.WriteFile(filepath.Join(bundleDir, "annotation.md"), []byte(md), 0o644); err != nil {
		jsonErr(w, "failed to write annotation.md", http.StatusInternalServerError)
		return
	}

	relPath := filepath.ToSlash(filepath.Join(".taskyou", "annotations", bundleName, "annotation.md"))

	nudged := false
	if task.ClaudePaneID != "" && s.runner != nil {
		nudge := fmt.Sprintf("[ty-chrome] Browser annotations received for this task. Read %s", relPath)
		if hasScreenshot {
			nudge += " and view the screenshot.png next to it"
		}
		nudge += ", then make the requested changes."
		if err := s.runner.Run("tmux", "send-keys", "-t", task.ClaudePaneID, "-l", nudge); err == nil {
			if err := s.runner.Run("tmux", "send-keys", "-t", task.ClaudePaneID, "Enter"); err == nil {
				nudged = true
			}
		}
	}

	jsonOK(w, map[string]interface{}{"ok": true, "path": relPath, "nudged": nudged})
}

func buildAnnotationMarkdown(req *annotationsRequest, hasScreenshot bool) string {
	var b strings.Builder
	b.WriteString("# Browser annotations\n\n")
	fmt.Fprintf(&b, "- **Page:** %s\n", req.URL)
	if req.Title != "" {
		fmt.Fprintf(&b, "- **Title:** %s\n", req.Title)
	}
	fmt.Fprintf(&b, "- **Captured:** %s\n", time.Now().Format(time.RFC3339))
	if req.Viewport.Width > 0 {
		fmt.Fprintf(&b, "- **Viewport:** %dx%d @%gx\n", req.Viewport.Width, req.Viewport.Height, req.Viewport.DPR)
	}
	if hasScreenshot {
		b.WriteString("- **Screenshot:** screenshot.png (numbered markers match the annotations below)\n")
	}
	b.WriteString("\n")
	if req.Instruction != "" {
		fmt.Fprintf(&b, "## Instruction\n\n%s\n\n", req.Instruction)
	}
	b.WriteString("## Annotations\n\n")
	for _, a := range req.Annotations {
		fmt.Fprintf(&b, "### %d. %s\n\n", a.Label, a.Kind)
		if a.Comment != "" {
			fmt.Fprintf(&b, "> %s\n\n", a.Comment)
		}
		if a.Selector != "" {
			fmt.Fprintf(&b, "- **Selector:** `%s`\n", a.Selector)
		}
		if a.Tag != "" {
			fmt.Fprintf(&b, "- **Element:** `<%s>`\n", a.Tag)
		}
		if a.Text != "" {
			fmt.Fprintf(&b, "- **Text:** %q\n", a.Text)
		}
		if a.Rect != nil {
			fmt.Fprintf(&b, "- **Position:** x=%.0f y=%.0f w=%.0f h=%.0f (CSS px, page coords)\n", a.Rect.X, a.Rect.Y, a.Rect.W, a.Rect.H)
		}
		if len(a.Styles) > 0 {
			b.WriteString("- **Computed styles:**")
			for k, v := range a.Styles {
				fmt.Fprintf(&b, " %s=%s;", k, v)
			}
			b.WriteString("\n")
		}
		if a.HTML != "" {
			fmt.Fprintf(&b, "\n```html\n%s\n```\n", a.HTML)
		}
		b.WriteString("\n")
	}
	return b.String()
}
```

Register in `server.go` after the `/input` route:

```go
	mux.HandleFunc("POST /api/tasks/{id}/annotations", s.handleTaskAnnotations)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/web/ -v`
Expected: all PASS

- [ ] **Step 5: Commit** — `feat(web): annotation intake endpoint for ty-chrome`

---

### Task 3: Extension scaffold — manifest + service worker

**Files:**
- Create: `extensions/ty-chrome/manifest.json`
- Create: `extensions/ty-chrome/sw.js`
- Create: `extensions/ty-chrome/README.md`
- Create: `extensions/ty-chrome/icons/icon16.png`, `icon48.png`, `icon128.png` (solid teal squares, generated)

- [ ] **Step 1: manifest.json**

```json
{
  "manifest_version": 3,
  "name": "TaskYou — Live Annotate",
  "version": "0.1.0",
  "description": "Point, click, and annotate pages served by a taskyou task; deliver context straight to the task's executor.",
  "permissions": ["storage", "tabs", "sidePanel", "scripting"],
  "host_permissions": ["http://localhost/*", "http://127.0.0.1/*"],
  "background": { "service_worker": "sw.js" },
  "action": { "default_title": "TaskYou Annotate" },
  "side_panel": { "default_path": "sidepanel.html" },
  "icons": { "16": "icons/icon16.png", "48": "icons/icon48.png", "128": "icons/icon128.png" }
}
```

- [ ] **Step 2: sw.js** — responsibilities and message protocol (complete contract):

State: `serverUrl` from `chrome.storage.local` (default `http://127.0.0.1:8080`); in-memory `Map tabId → task` of port matches.

Matching: on `tabs.onActivated` / `tabs.onUpdated(status=complete)` and on demand: if tab URL host is localhost/127.0.0.1 with a port, fetch `${serverUrl}/api/tasks?status=processing` and `?status=blocked`, find task with `task.port === Number(url.port)`; cache and set action badge to the task id (clear otherwise).

Action click: `chrome.sidePanel.open({tabId})` (also set `sidePanel.setPanelBehavior({openPanelOnActionClick: true})` at startup).

`chrome.runtime.onMessage` routes (all return Promises via `sendResponse`):
- `{type:"getState", tabId}` → `{serverUrl, connected, task, annotationCount}` (connected = `/api/status` ok; task = port match for tab; annotationCount asked from content script via `tabs.sendMessage`, 0 if absent)
- `{type:"setServerUrl", url}` → persist, re-match
- `{type:"listCandidateTasks"}` → processing+blocked tasks array
- `{type:"pickTask", tabId, taskId}` → manual override for tab (stored in match map)
- `{type:"startAnnotate", tabId}` → `chrome.scripting.executeScript({target:{tabId}, files:["content.js"]})` then `tabs.sendMessage(tabId, {type:"ty-enter-select"})`
- `{type:"capture", windowId}` → `chrome.tabs.captureVisibleTab(windowId, {format:"png"})` → dataUrl (catch → null)
- `{type:"sendAnnotations", tabId, payload}` → resolve task for tab; capture screenshot; POST `${serverUrl}/api/tasks/{id}/annotations` with `{...payload, screenshot}`; on success tell content script `{type:"ty-clear"}`; return `{ok, nudged, path, taskId}` or `{error}`
- `{type:"getOutput", taskId, lines}` → GET `/api/tasks/{taskId}/output?lines=` → `{output}` (404/410 → `{gone:true}`)
- `{type:"taskInput", taskId, message}` → POST `/api/tasks/{taskId}/input` `{message, enter:true}`
- `{type:"annotationsChanged", count}` (from content) → rebroadcast to side panel via `chrome.runtime.sendMessage({type:"ty-annotations-count", count})`

- [ ] **Step 3: README.md** — install (chrome://extensions → Load unpacked), prerequisite (`ty serve` running, default port 8080), usage walkthrough, API endpoints used, screenshot.

- [ ] **Step 4: Commit** — `feat(ty-chrome): extension scaffold, manifest + service worker`

---

### Task 4: Content-script annotation overlay

**Files:**
- Create: `extensions/ty-chrome/content.js`

Single file, idempotent (guard `window.__tyAnnotate`), all UI inside a closed shadow root attached to a fixed-position host div (z-index 2147483647). No page CSS leakage either direction.

- [ ] **Step 1: Write content.js** with this structure:

Toolbar (bottom-center pill): mode buttons **Select**, **Box**, **Note**, count chip, **Send**, **✕** (clear+exit). Esc exits current mode.

State: `annotations: []` each `{kind, label, selector?, tag?, text?, html?, rect?, styles?, comment, markerEl}`.

Select mode: `mouseover` → outline via absolutely-positioned highlight box (not element style mutation); `click` (capture-phase, preventDefault) → snapshot element, show comment popover anchored at element; Save → push annotation + numbered marker pinned at element's top-right (page coords, repositioned on scroll/resize via `requestAnimationFrame` loop or absolute positioning in a full-page overlay layer).

Box mode: full-page transparent canvas-like div capturing pointer events; drag → dashed rect; release → comment popover; Save → translucent rect + numbered marker persists.

Note mode: popover center-screen, Save → marker pinned top-left stack.

Element snapshot (exact algorithm):

```js
function cssPath(el) {
  if (el.id) return `#${CSS.escape(el.id)}`;
  const parts = [];
  while (el && el.nodeType === 1 && el !== document.body) {
    let part = el.localName;
    const stable = [...el.classList].filter(c => /^[a-zA-Z][\w-]*$/.test(c)).slice(0, 2);
    if (stable.length) part += '.' + stable.map(CSS.escape).join('.');
    const siblings = el.parentElement ? [...el.parentElement.children].filter(s => s.localName === el.localName) : [];
    if (siblings.length > 1) part += `:nth-of-type(${siblings.indexOf(el) + 1})`;
    parts.unshift(part);
    if (el.id) { parts.unshift(`#${CSS.escape(el.id)}`); break; }
    el = el.parentElement;
  }
  return parts.join(' > ');
}

const STYLE_KEYS = ['color','backgroundColor','fontSize','fontWeight','fontFamily','display','position','margin','padding'];
function snapshotElement(el) {
  const cs = getComputedStyle(el);
  const r = el.getBoundingClientRect();
  return {
    kind: 'element',
    selector: cssPath(el),
    tag: el.localName,
    text: (el.innerText || '').trim().slice(0, 200),
    html: el.outerHTML.length > 1500 ? el.outerHTML.slice(0, 1500) + '…' : el.outerHTML,
    rect: { x: r.x + scrollX, y: r.y + scrollY, w: r.width, h: r.height },
    styles: Object.fromEntries(STYLE_KEYS.map(k => [k, cs[k]])),
  };
}
```

Markers: 22px circles, teal (#0d9488), white number, absolutely positioned in a full-page overlay div (`position:absolute; top/left from rect`), so `captureVisibleTab` bakes them into the screenshot.

Messages: listens for `ty-enter-select`, `ty-clear`, `ty-get-count`, `ty-collect` (→ returns `{url, title, viewport, annotations}` without markerEl). Sends `{type:"annotationsChanged", count}` to SW after every add/remove. Send button → `chrome.runtime.sendMessage({type:"sendAnnotations", tabId: undefined, payload: collect()})` (SW uses sender.tab.id); toolbar briefly hides during capture? **No** — markers must stay visible; only the comment popover hides. Toast shows result ("Sent to task #N — executor nudged" / error).

- [ ] **Step 2: Manual smoke check** — `chrome://extensions` load unpacked, inject on any localhost page, place one of each annotation kind. (Full validation deferred to Task 6 demo.)

- [ ] **Step 3: Commit** — `feat(ty-chrome): annotation overlay content script`

---

### Task 5: Side panel

**Files:**
- Create: `extensions/ty-chrome/sidepanel.html`
- Create: `extensions/ty-chrome/sidepanel.js`
- Create: `extensions/ty-chrome/sidepanel.css`

- [ ] **Step 1: Write the three files.** Layout top→bottom:

1. Header: status dot (green/red), "TaskYou" title, gear toggle revealing server URL input (Enter saves via `setServerUrl`).
2. Task card: matched task `#id title` + status pill + `:port` + branch; or dropdown of candidate tasks ("No task matches this tab's port — pick one") + Refresh.
3. Annotate row: **Annotate this page** button (`startAnnotate`), annotation count (live via `ty-annotations-count`).
4. Instruction textarea (placeholder "Overall instruction for the executor (optional)") + **Send annotations** button → `{type:"sendAnnotations"}` with instruction merged into payload; result line under button.
5. Executor section (the teleport): `<pre>` console (dark bg, monospace, autoscroll-to-bottom unless user scrolled up), polls `getOutput` every 2.5s via `setInterval` only while `document.visibilityState === 'visible'` and a task is matched; ANSI stripped:

```js
const stripAnsi = s => s.replace(/\x1b\[[0-9;?]*[ -\/]*[@-~]/g, '').replace(/\x1b\][^\x07]*(\x07|\x1b\\)/g, '');
```

6. Follow-up input (single line) + Send → `taskInput`.

Styling: compact, system font stack, teal accent #0d9488 matching markers, dark console block. No frameworks.

`tabId` resolution: side panel calls `chrome.tabs.query({active:true, currentWindow:true})` and re-resolves on `tabs.onActivated`.

- [ ] **Step 2: Commit** — `feat(ty-chrome): side panel with executor stream`

---

### Task 6: End-to-end demo (isolated instance)

**Files:**
- Create: `/tmp/tychrome-demo/` (throwaway: DB, projects, demo app, scripts)
- Create: `scripts/demo/ty-chrome-demo.md` only if worth keeping (else demo stays in /tmp)

- [ ] **Step 1: Build `ty` from this branch:** `go build -o /tmp/tychrome-demo/ty ./cmd/task`
- [ ] **Step 2: Isolated instance** per QA harness rules (separate tmux server!):
  - `export WORKTREE_DB_PATH=/tmp/tychrome-demo/tasks.db`
  - All tmux on socket `-L tychrome-demo` (shadow function in script)
  - Demo app: git repo `/tmp/tychrome-demo/projects/demo-app` with `index.html` + simple HTTP server on port 3142 (python3)
  - Seed project + task via the built `ty` CLI / sqlite3: task with `port=3142`, `worktree_path=<demo-app>`, `status=processing`, `claude_pane_id=%N` of a real pane on the isolated tmux server
  - `WORKTREE_DB_PATH=... /tmp/tychrome-demo/ty serve --port 8765 &`
- [ ] **Step 3: Verify API by hand:** `curl localhost:8765/api/tasks` shows port/worktree/has_executor; `curl -X POST .../annotations` with a small JSON → file lands in demo-app, nudge text appears in pane (`tmux -L tychrome-demo capture-pane`).
- [ ] **Step 4: Playwright persistent context** (node script, `--load-extension`): open `http://localhost:3142`, open `chrome-extension://<id>/sidepanel.html` in second tab (point server URL at :8765), drive annotate flow on tab 1 (select element, comment, send), screenshot each stage + the tmux pane content + the annotation.md.
- [ ] **Step 5 (stretch): real executor loop** — run actual `claude` in the pane, let it apply the requested edit to index.html, screenshot the changed page.
- [ ] **Step 6: Tear down** isolated tmux server + background processes.

---

### Task 7: Lint, full test pass, final commit

- [ ] **Step 1:** `go test ./internal/web/ ./internal/db/` green; `gofmt -l internal/web` empty
- [ ] **Step 2:** `git add extensions/ty-chrome docs/superpowers/plans` etc.; commit `feat(ty-chrome): browser annotation extension + demo`

---

## Self-review

- **Spec coverage:** task JSON enrichment (T1), annotation endpoint + gitignore + nudge + fallbacks (T2), SW/tab-matching/screenshot (T3), overlay modes + selectors + markers (T4), side panel teleport + follow-up input (T5), error paths (T2 tests + SW error returns), demo (T6). Out-of-scope items remain out.
- **Placeholders:** none ("write the files to this contract" tasks carry the full contract: protocol, algorithms, layout).
- **Type consistency:** `handleTaskAnnotations` name matches route + tests; payload field names match between content.js `collect()`, SW POST body, and Go `annotationsRequest` (url/title/viewport/instruction/annotations/screenshot; rect x/y/w/h lowercase via JSON tags); `resp.Path` is worktree-relative in both tests and SW usage.
