package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	".git", "package.json", "go.mod", "Cargo.toml", "pyproject.toml",
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
	if clean == "/" || clean == filepath.Clean(os.TempDir()) || strings.HasPrefix(clean, "/tmp") {
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
