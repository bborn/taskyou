package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/github"
)

const MaxPreviewBytes = 256 * 1024
const MaxEntries = 500

func (s *Service) root(t *db.Task) (string, error) {
	if t.PlacementTarget != "" && t.PlacementTarget != "local" {
		root, _, err := s.DB.GetTaskRemoteWorktree(t.ID)
		if err != nil {
			return "", err
		}
		if root == "" {
			return "", fmt.Errorf("remote workspace is unavailable")
		}
		return root, nil
	}
	if t.WorktreePath != "" {
		return t.WorktreePath, nil
	}
	if t.Project != "" {
		p, err := s.DB.GetProjectByName(t.Project)
		if err != nil {
			return "", err
		}
		if p != nil && p.Path != "" {
			return p.Path, nil
		}
	}
	return "", fmt.Errorf("this task has no workspace; choose a project or start the task")
}

func (s *Service) files(ctx context.Context, t *db.Task, resource string) (Content, error) {
	return s.readResource(ctx, t, resource, true)
}
func (s *Service) file(ctx context.Context, t *db.Task, resource string) (Content, error) {
	return s.readResource(ctx, t, resource, false)
}
func (s *Service) readResource(ctx context.Context, t *db.Task, resource string, directory bool) (Content, error) {
	root, err := s.root(t)
	if err != nil {
		return Content{}, err
	}
	if t.PlacementTarget != "" && t.PlacementTarget != "local" {
		return remoteResource(ctx, t.PlacementTarget, root, resource, directory)
	}
	// os.Root confines even symlink resolution and concurrent path replacement.
	dir, err := os.OpenRoot(root)
	if err != nil {
		return Content{}, fmt.Errorf("workspace unavailable: %w", err)
	}
	defer dir.Close()
	f, err := dir.OpenFile(resource, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Content{}, err
	}
	if directory {
		if !info.IsDir() {
			return Content{}, fmt.Errorf("not a directory")
		}
		entries, err := f.ReadDir(MaxEntries + 1)
		if err != nil && err != io.EOF {
			return Content{}, err
		}
		if len(entries) > MaxEntries {
			return Content{}, fmt.Errorf("directory contains more than %d entries; open a smaller subdirectory", MaxEntries)
		}
		c := Content{Kind: "files", Entries: []Entry{}}
		for _, e := range entries {
			if e.Name() == ".git" {
				continue
			}
			c.Entries = append(c.Entries, Entry{path.Join(resource, e.Name()), e.Name(), e.IsDir()})
		}
		sort.Slice(c.Entries, func(i, j int) bool {
			a, b := c.Entries[i], c.Entries[j]
			if a.Directory != b.Directory {
				return a.Directory
			}
			return a.Name < b.Name
		})
		return c, nil
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("only regular text files can be previewed")
	}
	if info.Size() > MaxPreviewBytes {
		return Content{}, fmt.Errorf("file exceeds the %d KiB preview limit", MaxPreviewBytes/1024)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxPreviewBytes+1))
	if err != nil {
		return Content{}, err
	}
	return textContent(resource, data)
}
func textContent(resource string, data []byte) (Content, error) {
	if len(data) > MaxPreviewBytes {
		return Content{}, fmt.Errorf("file exceeds the preview limit")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return Content{}, fmt.Errorf("binary files cannot be previewed")
	}
	kind := "text"
	if ext := strings.ToLower(path.Ext(resource)); ext == ".md" || ext == ".markdown" {
		kind = "markdown"
	}
	return Content{Kind: kind, Text: string(data)}, nil
}

func (s *Service) pullRequest(_ context.Context, t *db.Task, _ string) (Content, error) {
	p := github.UnmarshalPRInfo(t.PRInfoJSON)
	if p == nil {
		return Content{Kind: "markdown", Text: "## Pull request\n\nNo cached pull request details yet. PR information updates with the task's GitHub status.", URL: t.PRURL}, nil
	}
	return Content{Kind: "markdown", Text: fmt.Sprintf("## #%d · %s\n\n**State:** %s\n\n**Checks:** %s\n\n**Mergeability:** %s\n\n**Changes:** +%d / −%d\n\n_Cached GitHub status; updates with task refresh._", p.Number, p.Title, p.State, p.CheckState, p.Mergeable, p.Additions, p.Deletions), URL: p.URL}, nil
}

// Remote previews use the placement runner, never the local checkout. Every
// path component is opened relative to a directory fd with O_NOFOLLOW; remote
// symlinks are deliberately unavailable rather than risking an escape race.
const remoteReadScript = `import os,sys,json,stat
root,resource,mode=sys.argv[1:]
fd=os.open(os.path.expanduser(root),os.O_RDONLY|os.O_DIRECTORY)
try:
 for part in resource.split('/'):
  if part in ('','.'): continue
  if part=='..': raise ValueError('path outside workspace')
  n=os.open(part,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
  os.close(fd);fd=n
 info=os.fstat(fd)
 if mode=='directory':
  if not stat.S_ISDIR(info.st_mode): raise ValueError('not a directory')
  entries=[]
  with os.scandir(fd) as scan:
   for e in scan:
    if e.name=='.git': continue
    entries.append(dict(path=os.path.normpath(resource+'/'+e.name),name=e.name,directory=e.is_dir(follow_symlinks=False)))
    if len(entries)>500: raise ValueError('directory exceeds 500 entries; open a smaller subdirectory')
  entries.sort(key=lambda e:(not e['directory'],e['name']))
  print(json.dumps(dict(kind='files',entries=entries)))
 else:
  if not stat.S_ISREG(info.st_mode): raise ValueError('only regular text files can be previewed')
  if info.st_size>262144: raise ValueError('file exceeds 256 KiB preview limit')
  with os.fdopen(os.dup(fd),'rb') as f: data=f.read(262145)
  if len(data)>262144: raise ValueError('file exceeds preview limit')
  text=data.decode('utf-8')
  if '\x00' in text: raise ValueError('binary files cannot be previewed')
  print(json.dumps(dict(kind='markdown' if resource.lower().endswith(('.md','.markdown')) else 'text',text=text)))
finally: os.close(fd)
`

func remoteResource(ctx context.Context, host, root, resource string, directory bool) (Content, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mode := "file"
	if directory {
		mode = "directory"
	}
	cmd := (executor.RemoteRunner{Host: host}).Command(ctx, "", "python3", "-c", remoteReadScript, root, resource, mode)
	out, err := cmd.Output()
	if err != nil {
		return Content{}, fmt.Errorf("remote preview unavailable (requires Python 3 and a readable workspace): %w", err)
	}
	var c Content
	err = json.Unmarshal(out, &c)
	return c, err
}
