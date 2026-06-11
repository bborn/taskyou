package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// browserRelay bridges the task executor and the ty-chrome extension. The
// extension long-polls for commands; the executor POSTs a command and blocks
// until the extension runs it in the user's live tab and posts the result.
type browserRelay struct {
	mu       sync.Mutex
	lastSeen map[int64]time.Time
	queues   map[int64]chan *browserCmd
	waiters  map[string]chan json.RawMessage
	seq      atomic.Int64
}

type browserCmd struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
}

const (
	browserSessionTTL = 35 * time.Second
	browserExecWait   = 25 * time.Second
	browserPollWait   = 20 * time.Second
)

func newBrowserRelay() *browserRelay {
	return &browserRelay{
		lastSeen: make(map[int64]time.Time),
		queues:   make(map[int64]chan *browserCmd),
		waiters:  make(map[string]chan json.RawMessage),
	}
}

func (r *browserRelay) touch(taskID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSeen[taskID] = time.Now()
}

func (r *browserRelay) connected(taskID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.lastSeen[taskID]) < browserSessionTTL
}

func (r *browserRelay) queue(taskID int64) chan *browserCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.queues[taskID]
	if !ok {
		q = make(chan *browserCmd, 8)
		r.queues[taskID] = q
	}
	return q
}

func (r *browserRelay) addWaiter(id string) chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	r.mu.Lock()
	r.waiters[id] = ch
	r.mu.Unlock()
	return ch
}

func (r *browserRelay) removeWaiter(id string) {
	r.mu.Lock()
	delete(r.waiters, id)
	r.mu.Unlock()
}

func (r *browserRelay) resolve(id string, result json.RawMessage) bool {
	r.mu.Lock()
	ch, ok := r.waiters[id]
	if ok {
		delete(r.waiters, id)
	}
	r.mu.Unlock()
	if ok {
		ch <- result
	}
	return ok
}

// --- Handlers ---

type browserExecRequest struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

func (s *Server) handleBrowserExec(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	var req browserExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		jsonErr(w, "action required", http.StatusBadRequest)
		return
	}

	if !s.relay.connected(task.ID) {
		jsonErr(w, "no browser connected for this task (open the ty-chrome side panel on the page)", http.StatusServiceUnavailable)
		return
	}

	cmd := &browserCmd{
		ID:     fmt.Sprintf("c%d-%d", task.ID, s.relay.seq.Add(1)),
		Action: req.Action,
		Params: req.Params,
	}
	resultCh := s.relay.addWaiter(cmd.ID)

	select {
	case s.relay.queue(task.ID) <- cmd:
	default:
		s.relay.removeWaiter(cmd.ID)
		jsonErr(w, "browser command queue full", http.StatusTooManyRequests)
		return
	}

	select {
	case result := <-resultCh:
		jsonOK(w, map[string]interface{}{"ok": true, "result": s.materializeBrowserResult(task, req.Action, result)})
	case <-time.After(browserExecWait):
		s.relay.removeWaiter(cmd.ID)
		jsonErr(w, "browser did not respond (panel closed or tab busy)", http.StatusGatewayTimeout)
	}
}

// materializeBrowserResult writes bulky payloads (screenshots, DOM snapshots)
// into the task's worktree and replaces them with file paths the executor can
// Read directly.
func (s *Server) materializeBrowserResult(task *db.Task, action string, raw json.RawMessage) interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return json.RawMessage(raw)
	}

	root := s.resolveTaskRoot(task)
	if root == "" {
		return result
	}

	dir := filepath.Join(root, ".taskyou", "browser")
	writeFile := func(name string, data []byte) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ""
		}
		gitignore := filepath.Join(dir, ".gitignore")
		if _, err := os.Stat(gitignore); os.IsNotExist(err) {
			os.WriteFile(gitignore, []byte("*\n"), 0o644)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return ""
		}
		return filepath.ToSlash(filepath.Join(".taskyou", "browser", name))
	}

	switch action {
	case "screenshot":
		if data, _ := result["data"].(string); data != "" {
			if i := strings.Index(data, "base64,"); i >= 0 {
				data = data[i+len("base64,"):]
			}
			if png, err := base64.StdEncoding.DecodeString(data); err == nil && len(png) > 0 {
				if p := writeFile(fmt.Sprintf("screenshot-%d.png", time.Now().UnixMilli()), png); p != "" {
					delete(result, "data")
					result["path"] = p
				}
			}
		}
	case "snapshot":
		if html, _ := result["html"].(string); html != "" {
			if p := writeFile(fmt.Sprintf("snapshot-%d.html", time.Now().UnixMilli()), []byte(html)); p != "" {
				delete(result, "html")
				result["path"] = p
			}
		}
	}
	return result
}

func (s *Server) resolveTaskRoot(task *db.Task) string {
	root := task.WorktreePath
	if root == "" && task.Project != "" {
		if p, _ := s.db.GetProjectByName(task.Project); p != nil {
			root = p.Path
		}
	}
	if root == "" {
		return ""
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return ""
	}
	return root
}

func (s *Server) handleBrowserPoll(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	s.relay.touch(task.ID)
	s.ensureBrowserHowto(task)

	select {
	case cmd := <-s.relay.queue(task.ID):
		jsonOK(w, cmd)
	case <-time.After(browserPollWait):
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
	}
}

type browserResultRequest struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

func (s *Server) handleBrowserResult(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTask(w, r); !ok {
		return
	}

	var req browserResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		jsonErr(w, "id and result required", http.StatusBadRequest)
		return
	}

	if !s.relay.resolve(req.ID, req.Result) {
		jsonErr(w, "no waiter for command (executor timed out?)", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// ensureBrowserHowto drops a personalized cheat-sheet into the worktree the
// first time a browser connects, so the executor can discover the bridge.
func (s *Server) ensureBrowserHowto(task *db.Task) {
	root := s.resolveTaskRoot(task)
	if root == "" {
		return
	}
	dir := filepath.Join(root, ".taskyou", "browser")
	path := filepath.Join(dir, "HOWTO.md")
	if _, err := os.Stat(path); err == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	gitignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		os.WriteFile(gitignore, []byte("*\n"), 0o644)
	}

	base := s.baseURL
	endpoint := fmt.Sprintf("%s/api/tasks/%d/browser", base, task.ID)
	howto := fmt.Sprintf(`# Browser bridge — see and drive the user's live browser

The ty-chrome extension is connected to this task. Use the user's real browser
tab instead of launching your own. Every command is:

    curl -s -X POST %s \
      -H 'Content-Type: application/json' -d '{"action":"...","params":{...}}'

Responses are {"ok":true,"result":{...}}. If the side panel is closed you get
a 503; ask the user to open it.

## Actions

- **See the page** (most useful — do this first and after every change):
  '{"action":"screenshot"}' → result.path is a PNG in this worktree; Read it.
- **DOM snapshot**: '{"action":"snapshot"}' → result.path (full HTML), result.title, result.url
- **Console logs + JS errors**: '{"action":"console"}' → result.logs
- **Click**: '{"action":"click","params":{"selector":"#buy-btn"}}'
- **Type**: '{"action":"type","params":{"selector":"input[name=q]","text":"hello"}}'
- **Navigate** (localhost only): '{"action":"navigate","params":{"url":"http://localhost:%d/"}}'
- **Reload**: '{"action":"reload"}'

Screenshot after each interaction to verify what actually happened.
`, endpoint, task.Port)

	os.WriteFile(path, []byte(howto), 0o644)
}
