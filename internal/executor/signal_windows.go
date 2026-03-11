//go:build windows

package executor

import (
	"fmt"
	"os"
)

func sendSIGTSTP(_ *os.Process) error {
	return fmt.Errorf("suspend not supported on Windows")
}

func sendSIGCONT(_ *os.Process) error {
	return fmt.Errorf("resume not supported on Windows")
}
