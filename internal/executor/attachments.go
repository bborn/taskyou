package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// StageAttachments copies selected attachments into the execution host's worktree.
// nil IDs means all task attachments (launch/retry); explicit IDs are a reply's
// selection. Paths belong to that host. Files survive the agent turn for resume.
func StageAttachments(ctx context.Context, database *db.DB, taskID int64, workDir string, remote *RemoteRunner, ids []int64) ([]string, error) {
	var attachments []*db.Attachment
	var err error
	if ids == nil {
		attachments, err = database.ListAttachments(taskID)
	} else {
		seen := map[int64]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			a, e := database.GetAttachment(id)
			if e != nil {
				return nil, e
			}
			if a.TaskID != taskID {
				return nil, fmt.Errorf("attachment %d does not belong to task", id)
			}
			a.Data = nil // validate ownership without retaining every file in memory
			attachments = append(attachments, a)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(attachments) == 0 {
		return nil, nil
	}
	if workDir == "" {
		return nil, fmt.Errorf("task has no execution directory for attachments")
	}
	var root *os.Root
	if remote == nil {
		root, err = os.OpenRoot(workDir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
	}
	var paths []string
	for _, metadata := range attachments {
		a, err := database.GetAttachment(metadata.ID)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(a.Data)
		// IDs prevent duplicate names overwriting each other; hashes make paths immutable.
		name := filepath.Base(strings.ReplaceAll(a.Filename, "\\", "/"))
		name = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return '_'
			}
			return r
		}, name)
		if name == "." || name == ".." || name == "/" {
			name = "attachment"
		}
		rel := filepath.Join(".claude", "attachments", fmt.Sprintf("task-%d", taskID), fmt.Sprintf("%d-%x", a.ID, sum[:8]), name)
		if remote == nil {
			if err = root.MkdirAll(filepath.Dir(rel), 0700); err != nil {
				return nil, err
			}
			if err = writeStagedAttachment(root, rel, a.Data); err != nil {
				return nil, err
			}
			paths = append(paths, filepath.Join(workDir, rel))
		} else {
			transferCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			script := `set -eu; umask 077; mkdir -p "$1"; tmp=$(mktemp "$1/.upload-XXXXXX"); trap 'rm -f "$tmp"' EXIT; cat > "$tmp"; if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp"); else actual=$(shasum -a 256 "$tmp"); fi; test "${actual%% *}" = "$3"; mv -f "$tmp" "$2"`
			cmd := remote.Command(transferCtx, workDir, "sh", "-c", script, "attachment", filepath.Dir(rel), rel, fmt.Sprintf("%x", sum))
			cmd.Stdin = bytes.NewReader(a.Data)
			err = cmd.Run()
			cancel()
			if err != nil {
				return nil, fmt.Errorf("transfer %s to %s: %w", a.Filename, remote.Host, err)
			}
			paths = append(paths, rel)
		}
	}
	return paths, nil
}

// AttachmentPrompt uses ordinary file paths understood by every executor.
func AttachmentPrompt(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Attached files\nRead these files in the task's execution directory:\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "- %q\n", p)
	}
	return b.String()
}

// Publish execution copies atomically so simultaneous replies never expose a
// partially written file to an agent already reading the same attachment.
func writeStagedAttachment(root *os.Root, path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), ".upload-"+rand.Text())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, path)
}
