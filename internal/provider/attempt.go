package provider

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

type limitOutput struct {
	data     []byte
	max      int
	overflow bool
}

func (w *limitOutput) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > w.max {
		w.overflow = true
		return len(p), errors.New("control output overflow")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

type tailOutput struct{ data []byte }

func (w *tailOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n >= 64<<10 {
		w.data = append(w.data[:0], p[n-(64<<10):]...)
	} else {
		excess := len(w.data) + n - (64 << 10)
		if excess > 0 {
			w.data = w.data[excess:]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}

type attemptRequest struct {
	kind           string
	args, extra    []string
	stdin          io.Reader
	stdout, stderr io.Writer
	deadline       time.Duration
}

func (p Provider) attempt(ctx context.Context, r *state.Record, request attemptRequest) error {
	kind, args, extra := request.kind, request.args, request.extra
	stdin, stdout, stderr, deadline := request.stdin, request.stdout, request.stderr, request.deadline
	a, e := p.admitAttempt(r, kind)
	if e != nil {
		return e
	}
	childCtx := ctx
	var cancel context.CancelFunc
	if deadline > 0 {
		childCtx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	proc, e := p.Backend.Start(
		childCtx,
		sbx.StartRequest{Args: args, Extra: extra, Stdin: stdin, Stdout: stdout, Stderr: stderr},
	)
	if e != nil {
		return p.failedStartAttempt(a, e)
	}
	now := time.Now().UTC()
	pid := proc.PID()
	a.Phase = phaseStarted
	a.StartedAt = &now
	a.DiagnosticPID = &pid
	a.DiagnosticStartIdentity = now.Format(time.RFC3339Nano)
	if e = p.Store.SaveAttempt(a); e != nil {
		return p.unpersistedStartAttempt(ctx, proc, a, e)
	}
	return p.finishAttempt(childCtx, proc, a)
}

func (p Provider) admitAttempt(r *state.Record, kind string) (state.Attempt, error) {
	aID, e := state.Token()
	if e != nil {
		return state.Attempt{}, compat.Wrap("E_STORAGE", e)
	}
	a := state.Attempt{
		SchemaVersion: 1, AttemptID: aID, MachineKey: r.MachineKey,
		Generation: r.Generation, SandboxName: r.SandboxName, Epoch: r.Epoch,
		Kind: kind, Phase: "admitted", Outcome: outcomeUnknown,
	}
	if r.Operation != nil {
		a.OperationID = r.Operation.ID
		r.Operation.LastAttemptID = aID
		if e = p.Store.Save(*r); e != nil {
			return a, e
		}
	}
	return a, p.Store.SaveAttempt(a)
}

func (p Provider) failedStartAttempt(a state.Attempt, startErr error) error {
	a.Phase = phaseClientReaped
	a.Outcome = "start_failed"
	now := time.Now().UTC()
	a.CompletedAt = &now
	if saveErr := p.Store.SaveAttempt(a); saveErr != nil {
		return errors.Join(startErr, saveErr)
	}
	return startErr
}

func (p Provider) unpersistedStartAttempt(
	ctx context.Context,
	proc *sbx.Process,
	a state.Attempt,
	saveErr error,
) error {
	cancelled, stop := context.WithCancel(ctx)
	stop()
	waitErr := proc.Wait(cancelled)
	if errorCode(waitErr) != codeUncleanAttempt {
		completed := time.Now().UTC()
		a.Phase = phaseClientReaped
		a.Outcome = outcomeCancelled
		a.CompletedAt = &completed
	}
	if errors.Is(waitErr, context.Canceled) && errorCode(waitErr) != "E_STORAGE" {
		waitErr = nil
	}
	return errors.Join(saveErr, waitErr, p.Store.SaveAttempt(a))
}

func (p Provider) finishAttempt(ctx context.Context, proc *sbx.Process, a state.Attempt) error {
	e := proc.Wait(ctx)
	if errorCode(e) == codeUncleanAttempt {
		return e
	}
	completed := time.Now().UTC()
	a.CompletedAt = &completed
	a.Phase = phaseClientReaped
	if ctx.Err() != nil {
		a.Outcome = outcomeCancelled
	} else {
		a.Outcome = outcomeNormalExit
	}
	if code, ok := sbx.ExitCode(e); ok {
		a.ExitCode = &code
	} else if e == nil {
		zero := 0
		a.ExitCode = &zero
	}
	if saveErr := p.Store.SaveAttempt(a); saveErr != nil {
		return errors.Join(e, saveErr)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e
}

type controlRequest struct {
	kind        string
	args, extra []string
	timeout     time.Duration
}

func (p Provider) controlAttempt(
	ctx context.Context,
	r *state.Record,
	request controlRequest,
) ([]byte, error) {
	kind, args, extra, timeout := request.kind, request.args, request.extra, request.timeout
	stdout := &limitOutput{max: 16 << 20}
	stderr := &tailOutput{}
	e := p.attempt(
		ctx,
		r,
		attemptRequest{
			kind:     kind,
			args:     args,
			extra:    extra,
			stdout:   stdout,
			stderr:   stderr,
			deadline: timeout,
		},
	)
	if stdout.overflow {
		return nil, compat.Fail(codeSBXSchema, "sbx control output exceeded 16 MiB")
	}
	if e != nil {
		return nil, p.quotedError(ctx, kind, e)
	}
	return stdout.data, nil
}
