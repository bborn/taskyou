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
			keys := make([]string, 0, len(a.Styles))
			for k := range a.Styles {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("- **Computed styles:**")
			for _, k := range keys {
				fmt.Fprintf(&b, " %s=%s;", k, a.Styles[k])
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
