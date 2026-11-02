package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const maxStdout = 16 << 20
const maxStderr = 64 << 10

type Client struct {
	Path  string
	Store state.Store
}
type bounded struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *bounded) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.overflow = true
		return len(p), fmt.Errorf("control output exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
}

type stderrTail struct{ data []byte }

func (b *stderrTail) Write(p []byte) (int, error) {
	n := len(p)
	if n >= maxStderr {
		b.data = append(b.data[:0], p[n-maxStderr:]...)
	} else {
		excess := len(b.data) + n - maxStderr
		if excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func observedLoginFailure(data []byte) bool {
	return strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "\n") == `error: not signed in to Docker
  try: sbx login`
}
func childEnv(extra []string) []string {
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+len(extra))
	for _, e := range inherited {
		key, _, _ := strings.Cut(e, "=")
		if strings.EqualFold(key, "COMMAND") || strings.EqualFold(key, "DEVSY_PROVIDER_CANCEL_FILE") || strings.EqualFold(key, "DOCKER_SANDBOXES_ROOT_SIZE") || strings.EqualFold(key, "DOCKER_SANDBOXES_DOCKER_SIZE") {
			continue
		}
		env = append(env, e)
	}
	return append(env, extra...)
}
func (c Client) newCommand(ctx context.Context, args []string, extra []string) (*exec.Cmd, func() error, error) {
	cwd, cleanup, e := c.Store.ControlCWD()
	if e != nil {
		return nil, nil, e
	}
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Dir = cwd
	cmd.Env = childEnv(extra)
	cmd.WaitDelay = 5 * time.Second
	return cmd, cleanup, nil
}
func (c Client) Control(ctx context.Context, limit time.Duration, args []string, extra []string) (data []byte, result error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd, cleanup, e := c.newCommand(ctx, args, extra)
	if e != nil {
		return nil, e
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			data = nil
			result = errors.Join(result, compat.Wrap("E_STORAGE", cleanupErr))
		}
	}()
	cmd.Stdin = nil
	stdout := &bounded{limit: maxStdout}
	stderr := &stderrTail{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	e = cmd.Run()
	if stdout.overflow {
		return nil, compat.Fail("E_SBX_SCHEMA", "control stdout exceeded 16 MiB")
	}
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if observedLoginFailure(stderr.data) {
			return nil, compat.Fail("E_SBX_AUTH", "Docker Sandboxes sign-in required; run sbx login outside Devsy")
		}
		return nil, fmt.Errorf("sbx %s failed: %w; stderr: %s", args[0], e, strings.TrimSpace(string(stderr.data)))
	}
	return stdout.Bytes(), nil
}

type Process struct {
	Cmd  *exec.Cmd
	done chan error
}

func (p *Process) PID() int { return p.Cmd.Process.Pid }
func (p *Process) Wait(ctx context.Context) error {
	select {
	case err := <-p.done:
		return err
	case <-ctx.Done():
		var terminationErr error
		if p.Cmd.Stdin != nil && p.Cmd.Stdin != os.Stdin {
			if input, ok := p.Cmd.Stdin.(io.Closer); ok {
				if err := input.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					terminationErr = errors.Join(terminationErr, compat.Wrap("E_STORAGE", err))
				}
			}
		}
		if err := interrupt(p.Cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
			terminationErr = errors.Join(terminationErr, compat.Wrap("E_SBX_UNAVAILABLE", err))
		}
		reaped := func(err error) error {
			var tagged *compat.Error
			if errors.As(err, &tagged) {
				terminationErr = errors.Join(terminationErr, err)
			}
			return errors.Join(ctx.Err(), terminationErr)
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case err := <-p.done:
			return reaped(err)
		case <-timer.C:
			if err := p.Cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				terminationErr = errors.Join(terminationErr, compat.Wrap("E_SBX_UNAVAILABLE", err))
			}
		}
		timer.Reset(5 * time.Second)
		select {
		case err := <-p.done:
			return reaped(err)
		case <-timer.C:
			return errors.Join(compat.Fail("E_UNCLEAN_ATTEMPT", "owned sbx client could not be reaped"), terminationErr)
		}
	}
}
func (c Client) Start(ctx context.Context, args []string, extra []string, stdin io.Reader, stdout, stderr io.Writer) (*Process, error) {
	cmd, cleanup, e := c.newCommand(context.Background(), args, extra)
	if e != nil {
		return nil, e
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if e = ctx.Err(); e != nil {
		return nil, errors.Join(e, compat.Wrap("E_STORAGE", cleanup()))
	}
	if e = cmd.Start(); e != nil {
		return nil, errors.Join(e, compat.Wrap("E_STORAGE", cleanup()))
	}
	p := &Process{Cmd: cmd, done: make(chan error, 1)}
	go func() {
		waitErr := cmd.Wait()
		p.done <- errors.Join(waitErr, compat.Wrap("E_STORAGE", cleanup()))
	}()
	return p, nil
}
func ExitCode(err error) (int, bool) {
	var x *exec.ExitError
	if errors.As(err, &x) {
		return x.ExitCode(), true
	}
	return 0, false
}
