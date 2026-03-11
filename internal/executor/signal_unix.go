//go:build !windows

package executor

import (
	"os"
	"syscall"
)

func sendSIGTSTP(proc *os.Process) error {
	return proc.Signal(syscall.SIGTSTP)
}

func sendSIGCONT(proc *os.Process) error {
	return proc.Signal(syscall.SIGCONT)
}
