package compat

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var Stderr = NewWarner(os.Stderr)

type Warner struct {
	mu   sync.Mutex
	out  io.Writer
	seen map[string]bool
}

func NewWarner(out io.Writer) *Warner {
	return &Warner{out: out, seen: map[string]bool{}}
}

func (w *Warner) Warn(message string) {
	if w == nil || message == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[message] {
		return
	}
	w.seen[message] = true
	_, _ = fmt.Fprintln(w.out, "warning: "+message)
}
