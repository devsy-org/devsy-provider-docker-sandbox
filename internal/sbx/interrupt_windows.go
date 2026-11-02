//go:build windows

package sbx

import (
	"os"
)

func interrupt(p *os.Process) error { return p.Kill() }
