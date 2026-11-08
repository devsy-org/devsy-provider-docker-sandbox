package sbx

import (
	"io"
	"sync"
)

type latchWriter struct {
	mu      sync.Mutex
	w       io.Writer
	latched bool
}

func newLatchWriter(w io.Writer) *latchWriter { return &latchWriter{w: w} }

func (l *latchWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.latched {
		return len(p), nil
	}
	return l.w.Write(p)
}

func (l *latchWriter) latch() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.latched = true
	l.mu.Unlock()
}
