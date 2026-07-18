package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
)

// runningService is one supervised plugin service process.
type runningService struct {
	plugin string
	name   string
	cmd    *exec.Cmd
}

// ServiceSet is the collection of plugin services the daemon started. Hold onto it
// for the daemon's lifetime and call Stop on shutdown.
type ServiceSet struct {
	logger *log.Logger
	procs  []*runningService
}

// StartServices starts every service declared by an installed plugin and returns a
// handle to stop them. Each service runs as `sh -c <command>` from its plugin dir
// (or Cwd, resolved relative to it), in its own process group so Stop can signal the
// whole tree. This is how a plugin folds a former "extension" sidecar under the
// daemon's lifecycle: it comes up with the daemon and goes down with it.
func StartServices(pluginsDir string, logger *log.Logger) *ServiceSet {
	set := &ServiceSet{logger: logger}
	plugins, _ := LoadPlugins(pluginsDir)
	for _, p := range plugins {
		for _, s := range p.Services {
			dir := p.Dir
			if s.Cwd != "" {
				dir = filepath.Join(p.Dir, s.Cwd)
			}
			cmd := exec.Command("sh", "-c", s.Command)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), s.Env...)
			// Own process group so Stop can signal the service and its children.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				if logger != nil {
					logger.Error("plugin service failed to start", "plugin", p.Name, "service", s.Name, "error", err)
				}
				continue
			}
			if logger != nil {
				logger.Info("plugin service started", "plugin", p.Name, "service", s.Name, "pid", cmd.Process.Pid)
			}
			set.procs = append(set.procs, &runningService{plugin: p.Name, name: s.Name, cmd: cmd})
		}
	}
	return set
}

// Count reports how many services are running.
func (s *ServiceSet) Count() int {
	if s == nil {
		return 0
	}
	return len(s.procs)
}

// Stop signals every supervised service's process group with SIGTERM, waits briefly
// for a graceful exit, then SIGKILLs anything still alive.
func (s *ServiceSet) Stop() {
	if s == nil || len(s.procs) == 0 {
		return
	}
	for _, rs := range s.procs {
		if rs.cmd.Process == nil {
			continue
		}
		_ = syscall.Kill(-rs.cmd.Process.Pid, syscall.SIGTERM)
		if s.logger != nil {
			s.logger.Info("plugin service stopping", "plugin", rs.plugin, "service", rs.name)
		}
	}
	done := make(chan struct{})
	go func() {
		for _, rs := range s.procs {
			_ = rs.cmd.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		for _, rs := range s.procs {
			if rs.cmd.Process != nil {
				_ = syscall.Kill(-rs.cmd.Process.Pid, syscall.SIGKILL)
			}
		}
	}
	s.procs = nil
}
