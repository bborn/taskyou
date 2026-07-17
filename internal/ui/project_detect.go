package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bborn/workflow/internal/ai"
	"github.com/bborn/workflow/internal/db"
)

// projectInstructionFiles are the files we look at, in priority order, to infer
// project instructions when offering to create a project for a git repo.
var projectInstructionFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"CLAUDE.local.md",
	".cursorrules",
	"README.md",
}

// maxInferredInstructionLen caps how much of an instruction file we import so we
// don't blow past the form's character limit or paste an entire README.
const maxInferredInstructionLen = 4000

// dirIsGitRepo reports whether dir is the root of a git repository.
func dirIsGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	// .git is usually a directory, but for worktrees/submodules it's a file
	// pointing at the real git dir. Either way it's a git repo.
	return info.IsDir() || info.Mode().IsRegular()
}

// inferProjectName derives a sensible project name from a directory path,
// falling back to the cleaned base name of the directory.
func inferProjectName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "project"
	}
	return base
}

// readProjectInstructions looks for a known instructions file in dir and returns
// its (possibly truncated) contents along with the file name it came from.
// Returns empty strings when nothing suitable is found.
func readProjectInstructions(dir string) (instructions string, sourceFile string) {
	for _, name := range projectInstructionFiles {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		if len(content) > maxInferredInstructionLen {
			content = strings.TrimSpace(content[:maxInferredInstructionLen]) + "\n\n…(truncated)"
		}
		return content, name
	}
	return "", ""
}

// projectMarkerFiles signal that a directory is a real project even without git.
var projectMarkerFiles = []string{
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml",
	"requirements.txt", "Gemfile", "pom.xml", "build.gradle", "composer.json",
	"AGENTS.md", "CLAUDE.md", ".cursorrules", "Makefile",
}

// denyListedHomeChildren are directory names that, directly under $HOME, are
// never project candidates (system / dumping-ground folders).
var denyListedHomeChildren = map[string]bool{
	"Desktop": true, "Documents": true, "Downloads": true, "Music": true,
	"Pictures": true, "Movies": true, "Public": true, "Library": true,
	"Applications": true,
}

// isProjectCandidate reports whether dir is worth proactively offering as a
// TaskYou project: it must not be a system/dumping dir, and must show at least
// one positive signal (git repo or a project marker file).
func isProjectCandidate(dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)

	// Deny-list: root, temp, $HOME itself, and bare home children.
	if clean == "/" || clean == filepath.Clean(os.TempDir()) || clean == "/tmp" {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if clean == home {
			return false
		}
		if filepath.Dir(clean) == home && denyListedHomeChildren[filepath.Base(clean)] {
			return false
		}
	}

	// Positive signal: git repo or any marker file present.
	if dirIsGitRepo(clean) {
		return true
	}
	for _, marker := range projectMarkerFiles {
		if _, err := os.Stat(filepath.Join(clean, marker)); err == nil {
			return true
		}
	}
	return false
}

// detectProjectFromDir builds a project pre-filled with values inferred from a
// candidate directory. Returns nil when dir is not a project candidate. Worktrees
// default ON only for git repos (git is optional; non-git projects skip worktrees).
func detectProjectFromDir(dir string) (project *db.Project, instructionSource string) {
	if !isProjectCandidate(dir) {
		return nil, ""
	}
	instructions, source := readProjectInstructions(dir)
	return &db.Project{
		Name:         inferProjectName(dir),
		Path:         filepath.Clean(dir),
		Instructions: instructions,
		UseWorktrees: dirIsGitRepo(dir),
	}, source
}

// projectDetectTitle returns the confirm-modal title, git-aware so we never call
// a non-git folder a "repo".
func projectDetectTitle(useWorktrees bool) string {
	if useWorktrees {
		return "Create a TaskYou project for this repo?"
	}
	return "Create a TaskYou project for this folder?"
}

// projectDetectDescription renders the body of the "New Project Detected" confirm
// card. Kept pure (no huh/AppModel deps) so the first-run copy is unit-testable,
// and phrased for a first-timer: git-aware ("repo" vs "folder") with the worktree
// jargon spelled out as what it actually means for their tasks.
func projectDetectDescription(project *db.Project, instructionSource string, inferencePending bool) string {
	var desc strings.Builder
	if inferencePending {
		desc.WriteString("✨ Inferring project details…\n\n")
	}
	kind := "folder"
	if project.UseWorktrees {
		kind = "git repo"
	}
	desc.WriteString(fmt.Sprintf("This %s looks like a project.\n\nName: %s\n", kind, project.Name))
	if project.Aliases != "" {
		desc.WriteString(fmt.Sprintf("Alias: %s\n", project.Aliases))
	}
	desc.WriteString(fmt.Sprintf("Path: %s\n", project.Path))
	if instructionSource != "" {
		desc.WriteString(fmt.Sprintf("Instructions: imported from %s\n", instructionSource))
	} else if project.Instructions != "" {
		desc.WriteString("Description: " + firstLine(project.Instructions) + "\n")
	}
	if project.UseWorktrees {
		desc.WriteString("Isolation: each task runs in its own git worktree\n")
	} else {
		desc.WriteString("Isolation: off — not a git repo, so tasks run in the folder directly\n")
	}
	desc.WriteString("\nYou can edit any of this later in Settings.")
	return desc.String()
}

// uniqueProjectName ensures the inferred name doesn't collide with an existing
// project name (or alias), appending a numeric suffix if needed.
func uniqueProjectName(database *db.DB, name string) string {
	if database == nil {
		return name
	}
	taken := func(candidate string) bool {
		p, err := database.GetProjectByName(candidate)
		return err == nil && p != nil
	}
	if !taken(name) {
		return name
	}
	for i := 2; i < 1000; i++ {
		candidate := name + "-" + strconv.Itoa(i)
		if !taken(candidate) {
			return candidate
		}
	}
	return name
}

// projectSuggestionDismissedKey is the settings key used to remember that the
// user declined the offer to create a project for a given path.
func projectSuggestionDismissedKey(path string) string {
	return "project_suggestion_dismissed:" + filepath.Clean(path)
}

// applyInferredMetadata overlays LLM-inferred fields onto a detected project.
// Empty inferred fields are ignored so rule-based defaults are never erased.
// The description fills Instructions only when no instructions were imported.
func applyInferredMetadata(p *db.Project, meta ai.ProjectMetadata) {
	if p == nil {
		return
	}
	if n := strings.TrimSpace(meta.Name); n != "" {
		p.Name = n
	}
	if a := strings.TrimSpace(meta.Alias); a != "" {
		p.Aliases = a
	}
	if d := strings.TrimSpace(meta.Description); d != "" && strings.TrimSpace(p.Instructions) == "" {
		p.Instructions = d
	}
}
