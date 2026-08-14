package db

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHomePath turns a leading "~" into the user's home directory.
//
// A project path reaches the DB from several places — the settings form, the
// project-detect flow, `ty project add`, a hand-edited row — and some of them
// store the shell's own shorthand, "~/Projects/foo". Nothing downstream runs
// through a shell: the executor and the pipeline hand these paths straight to
// exec.Command("git", "-C", dir, ...), where a literal "~" is just a directory
// name that does not exist. The failure is remote from its cause — a pipeline
// aborting with
//
//	fatal: cannot change to '~/Projects/rails/offerlab': No such file or directory
//
// and a GetProjectByPath that silently never matches, so project detection and
// project-local workflow dirs quietly do nothing.
//
// Callers used to be expected to remember config.GetProjectDir; the ones that
// read Project.Path directly (three of them, at the time of writing) did not.
// Normalizing on both write and read removes the chance to forget.
//
// A "~user/..." form is left alone: resolving another user's home is not
// something we can do reliably, and it has never been a path we support.
func ExpandHomePath(path string) string {
	p := strings.TrimSpace(path)
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// normalizePath rewrites the project's path in place to its expanded form.
func (p *Project) normalizePath() {
	if p == nil {
		return
	}
	p.Path = ExpandHomePath(p.Path)
}
