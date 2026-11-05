//go:build !windows

package sbx

import (
	"os"
	"syscall"
)

func interrupt(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
