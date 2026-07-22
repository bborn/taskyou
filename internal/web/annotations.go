package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	// Keep bundles out of git status without touching the repo's .gitignore.
	if err := os.MkdirAll(annoRoot, 0o755); err != nil {
		jsonErr(w, "failed to create annotation dir", http.StatusInternalServerError)
		return
	}
	gitignore := filepath.Join(annoRoot, ".gitignore")
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		os.WriteFile(gitignore, []byte("*\n"), 0o644)
	}

	relPath, err := s.stageAnnotations(task.ID, annoRoot, &req)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// The nudge is deferred until the coalesce window closes, so `nudged`
	// reports that a live executor is there to receive it rather than that
	// send-keys has already run.
	jsonOK(w, map[string]interface{}{
		"ok":     true,
		"path":   relPath,
		"nudged": task.ClaudePaneID != "" && s.runner != nil,
	})
}

// annotationCoalesceWindow is how long a bundle stays open for more
// submissions. Two clients can annotate the same task at once (two panels, or
// the panel and the ⌥S shortcut); without this each submission would fire its
// own nudge and the executor would get several prompts for one thought. Long
// enough to catch a double-send, short enough to feel immediate.
const annotationCoalesceWindow = 1200 * time.Millisecond

// pendingAnnotationBundle is one bundle directory still accepting submissions.
type pendingAnnotationBundle struct {
	dir     string
	relPath string
	subs    []*annotationsRequest
	shots   []string // screenshot filename per submission ("" if none)
	timer   *time.Timer
}

// stageAnnotations writes a submission into the task's open bundle, opening one
// if there isn't a live bundle, and (re)arms the flush timer. Returns the bundle
// path that the eventual nudge will point at.
func (s *Server) stageAnnotations(taskID int64, annoRoot string, req *annotationsRequest) (string, error) {
	s.annoMu.Lock()
	defer s.annoMu.Unlock()

	if s.annoPending == nil {
		s.annoPending = map[int64]*pendingAnnotationBundle{}
	}
	p := s.annoPending[taskID]
	if p == nil {
		// Uniquify: two tasks can share a worktree, and a fast client can land
		// twice in the same millisecond.
		base := time.Now().UnixMilli()
		var dir, name string
		for i := 0; ; i++ {
			name = fmt.Sprintf("%d", base+int64(i))
			dir = filepath.Join(annoRoot, name)
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				break
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("failed to create annotation dir")
		}
		p = &pendingAnnotationBundle{
			dir:     dir,
			relPath: filepath.ToSlash(filepath.Join(".taskyou", "annotations", name, "annotation.md")),
		}
		s.annoPending[taskID] = p
	}

	// First screenshot keeps the plain name the nudge and docs refer to.
	shot := ""
	if png := decodeScreenshot(req.Screenshot); len(png) > 0 {
		shot = "screenshot.png"
		if len(p.subs) > 0 {
			shot = fmt.Sprintf("screenshot-%d.png", len(p.subs)+1)
		}
		if os.WriteFile(filepath.Join(p.dir, shot), png, 0o644) != nil {
			shot = ""
		}
	}
	p.subs = append(p.subs, req)
	p.shots = append(p.shots, shot)

	md := buildAnnotationBundle(p.subs, p.shots)
	if err := os.WriteFile(filepath.Join(p.dir, "annotation.md"), []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("failed to write annotation.md")
	}

	window := s.annoWindow
	if window <= 0 {
		window = annotationCoalesceWindow
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(window, func() { s.flushAnnotations(taskID) })
	return p.relPath, nil
}

// flushAnnotations closes the task's bundle and nudges its executor once.
func (s *Server) flushAnnotations(taskID int64) {
	s.annoMu.Lock()
	p := s.annoPending[taskID]
	delete(s.annoPending, taskID)
	s.annoMu.Unlock()
	if p == nil {
		return
	}

	// Re-read the task: the pane can change while the window is open.
	task, err := s.db.GetTask(taskID)
	if err != nil || task == nil || task.ClaudePaneID == "" || s.runner == nil {
		return
	}

	hasScreenshot := false
	for _, name := range p.shots {
		if name != "" {
			hasScreenshot = true
			break
		}
	}
	nudge := fmt.Sprintf("[ty-chrome] Browser annotations received for this task. Read %s", p.relPath)
	if hasScreenshot {
		nudge += " and view the screenshot.png next to it"
	}
	if len(p.subs) > 1 {
		nudge += fmt.Sprintf(" (%d submissions, all in that one file)", len(p.subs))
	}
	nudge += ", then make the requested changes."
	if s.relay.connected(taskID) {
		nudge += " The user's live browser is connected — read .taskyou/browser/HOWTO.md to view and interact with the page directly."
	}

	// One nudge at a time: the literal text and its Enter must stay adjacent.
	s.nudgeMu.Lock()
	defer s.nudgeMu.Unlock()
	if err := s.runner.Run("tmux", "send-keys", "-t", task.ClaudePaneID, "-l", nudge); err != nil {
		return
	}
	_ = s.runner.Run("tmux", "send-keys", "-t", task.ClaudePaneID, "Enter")
}

func decodeScreenshot(raw string) []byte {
	if raw == "" {
		return nil
	}
	if i := strings.Index(raw, "base64,"); i >= 0 {
		raw = raw[i+len("base64,"):]
	}
	png, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	return png
}

// buildAnnotationBundle renders every submission in a bundle. A single
// submission renders exactly as it always has; coalesced ones get a section
// each, since their marker numbers restart per screenshot.
func buildAnnotationBundle(subs []*annotationsRequest, shots []string) string {
	if len(subs) == 1 {
		return buildAnnotationMarkdown(subs[0], shots[0] != "")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Browser annotations (%d submissions)\n\n", len(subs))
	b.WriteString("The user sent these together; treat them as one request.\n\n")
	for i, sub := range subs {
		fmt.Fprintf(&b, "---\n\n## Submission %d\n\n", i+1)
		if shots[i] != "" {
			fmt.Fprintf(&b, "Screenshot: %s (numbered markers match this submission's annotations)\n\n", shots[i])
		}
		b.WriteString(buildAnnotationMarkdown(sub, false))
	}
	return b.String()
}

func buildAnnotationMarkdown(req *annotationsRequest, hasScreenshot bool) string {
	var b strings.Builder
	b.WriteString("# Browser annotations\n\n")
	writeAnnotationBody(&b, req, hasScreenshot)
	return b.String()
}

func writeAnnotationBody(b *strings.Builder, req *annotationsRequest, hasScreenshot bool) {
	fmt.Fprintf(b, "- **Page:** %s\n", req.URL)
	if req.Title != "" {
		fmt.Fprintf(b, "- **Title:** %s\n", req.Title)
	}
	fmt.Fprintf(b, "- **Captured:** %s\n", time.Now().Format(time.RFC3339))
	if req.Viewport.Width > 0 {
		fmt.Fprintf(b, "- **Viewport:** %dx%d @%gx\n", req.Viewport.Width, req.Viewport.Height, req.Viewport.DPR)
	}
	if hasScreenshot {
		b.WriteString("- **Screenshot:** screenshot.png (numbered markers match the annotations below)\n")
	}
	b.WriteString("\n")
	if req.Instruction != "" {
		fmt.Fprintf(b, "## Instruction\n\n%s\n\n", req.Instruction)
	}
	b.WriteString("## Annotations\n\n")
	for _, a := range req.Annotations {
		fmt.Fprintf(b, "### %d. %s\n\n", a.Label, a.Kind)
		if a.Comment != "" {
			fmt.Fprintf(b, "> %s\n\n", a.Comment)
		}
		if a.Selector != "" {
			fmt.Fprintf(b, "- **Selector:** `%s`\n", a.Selector)
		}
		if a.Tag != "" {
			fmt.Fprintf(b, "- **Element:** `<%s>`\n", a.Tag)
		}
		if a.Text != "" {
			fmt.Fprintf(b, "- **Text:** %q\n", a.Text)
		}
		if a.Rect != nil {
			fmt.Fprintf(b, "- **Position:** x=%.0f y=%.0f w=%.0f h=%.0f (CSS px, page coords)\n", a.Rect.X, a.Rect.Y, a.Rect.W, a.Rect.H)
		}
		if len(a.Styles) > 0 {
			keys := make([]string, 0, len(a.Styles))
			for k := range a.Styles {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("- **Computed styles:**")
			for _, k := range keys {
				fmt.Fprintf(b, " %s=%s;", k, a.Styles[k])
			}
			b.WriteString("\n")
		}
		if a.HTML != "" {
			fmt.Fprintf(b, "\n```html\n%s\n```\n", a.HTML)
		}
		b.WriteString("\n")
	}
}
