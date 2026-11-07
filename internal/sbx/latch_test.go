package sbx

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const (
	observedCancelText = "error: context canceled\n"
	signalFakeSource   = `package main
import("os";"os/signal";"syscall")
func main(){
switch os.Args[len(os.Args)-1]{
case "plain":os.Stderr.WriteString("x")
case "before-and-after","after-only":
c:=make(chan os.Signal,1);signal.Notify(c,syscall.SIGTERM)
if os.Args[len(os.Args)-1]=="before-and-after"{os.Stderr.WriteString("x")}
os.Stdout.WriteString("ready\n")
<-c
os.Stderr.WriteString("error: context canceled\n")
os.Stdout.WriteString("bye\n")
os.Exit(1)}}`
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(b.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q, have %q", want, b.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type recordingWriter struct {
	mu     sync.Mutex
	chunks []string
	err    error
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.chunks = append(w.chunks, string(p))
	return len(p), nil
}

func TestLatchWriter(t *testing.T) {
	tests := []struct {
		name      string
		writes    []string
		latchAt   int
		wantChunk []string
	}{
		{"passes through before latch", []string{"a", "b"}, 2, []string{"a", "b"}},
		{"discards after latch", []string{"a", "b", "c"}, 1, []string{"a"}},
		{"discards everything when latched first", []string{"a", "b"}, 0, nil},
		{"discards observed text", []string{observedCancelText}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingWriter{}
			l := newLatchWriter(sink)
			for i, w := range tt.writes {
				if i == tt.latchAt {
					l.latch()
				}
				n, err := l.Write([]byte(w))
				if n != len(w) || err != nil {
					t.Fatalf("write %d: n=%d err=%v", i, n, err)
				}
			}
			if tt.latchAt >= len(tt.writes) {
				l.latch()
			}
			if strings.Join(sink.chunks, "|") != strings.Join(tt.wantChunk, "|") {
				t.Fatalf("got %q want %q", sink.chunks, tt.wantChunk)
			}
		})
	}
}

func TestLatchWriterPassesErrorsBeforeLatch(t *testing.T) {
	boom := errors.New("boom")
	l := newLatchWriter(&recordingWriter{err: boom})
	if _, err := l.Write([]byte("a")); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	l.latch()
	if n, err := l.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestLatchWriterNilLatch(*testing.T) {
	var l *latchWriter
	l.latch()
}

func TestLatchWriterConcurrent(t *testing.T) {
	sink := &syncBuffer{}
	l := newLatchWriter(sink)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if n, err := l.Write([]byte("z")); n != 1 || err != nil {
					t.Errorf("n=%d err=%v", n, err)
					return
				}
			}
		})
	}
	l.latch()
	wg.Wait()
	before := sink.String()
	if n, err := l.Write([]byte("tail")); n != 4 || err != nil || sink.String() != before {
		t.Fatalf("write after latch leaked: n=%d err=%v", n, err)
	}
}

func signalClient(t *testing.T) Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("signal fake needs POSIX SIGTERM delivery")
	}
	return Client{
		Path: buildFake(t, signalFakeSource),
		Store: state.Store{
			Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		},
	}
}

func runAndMaybeCancel(
	t *testing.T,
	program string,
	cancelRun bool,
) (stdout, stderr string, err error) {
	t.Helper()
	c := signalClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, errBuf := &syncBuffer{}, &syncBuffer{}
	p, e := c.Start(
		ctx,
		StartRequest{Args: ExecArgs(testOwned, program), Stdout: out, Stderr: errBuf},
	)
	if e != nil {
		t.Fatal(e)
	}
	if cancelRun {
		out.waitFor(t, "ready\n")
		if program == "before-and-after" {
			errBuf.waitFor(t, "x")
		}
		cancel()
	}
	err = p.Wait(ctx)
	return out.String(), errBuf.String(), err
}

func TestStderrAroundCancellation(t *testing.T) {
	tests := []struct {
		name       string
		program    string
		wantStderr string
		wantStdout string
		cancel     bool
	}{
		{"unchanged without cancel", "plain", "x", "", false},
		{"output before cancel survives", "before-and-after", "x", "ready\nbye\n", true},
		{"output only after cancel is dropped", "after-only", "", "ready\nbye\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runAndMaybeCancel(t, tt.program, tt.cancel)
			if tt.cancel != errors.Is(err, context.Canceled) || (!tt.cancel && err != nil) {
				t.Fatalf("wrong wait result: %v", err)
			}
			if _, ok := ExitCode(err); ok {
				t.Fatalf("cancellation result changed: %v", err)
			}
			if stderr != tt.wantStderr || stdout != tt.wantStdout {
				t.Fatalf("stdout %q stderr %q", stdout, stderr)
			}
		})
	}
}

func TestStartWithoutStderr(t *testing.T) {
	c := signalClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &syncBuffer{}
	p, e := c.Start(ctx, StartRequest{Args: ExecArgs(testOwned, "after-only"), Stdout: stdout})
	if e != nil {
		t.Fatal(e)
	}
	stdout.waitFor(t, "ready\n")
	cancel()
	if err := p.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong cancellation result: %v", err)
	}
}
