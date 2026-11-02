package provider

import (
	"context"
	"errors"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"io"
	"time"
)

type limitOutput struct {
	data     []byte
	max      int
	overflow bool
}

func (w *limitOutput) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > w.max {
		w.overflow = true
		return len(p), fmt.Errorf("control output overflow")
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
func (p Provider) attempt(ctx context.Context, r *state.Record, kind string, args, extra []string, stdin io.Reader, stdout, stderr io.Writer, deadline time.Duration) error {
	aID, e := state.Token()
	if e != nil {
		return compat.Wrap("E_STORAGE", e)
	}
	a := state.Attempt{SchemaVersion: 1, AttemptID: aID, MachineKey: r.MachineKey, Generation: r.Generation, SandboxName: r.SandboxName, Epoch: r.Epoch, Kind: kind, Phase: "admitted", Outcome: "unknown"}
	if r.Operation != nil {
		a.OperationID = r.Operation.ID
		r.Operation.LastAttemptID = aID
		if e = p.Store.Save(*r); e != nil {
			return e
		}
	}
	if e = p.Store.SaveAttempt(a); e != nil {
		return e
	}
	childCtx := ctx
	var cancel context.CancelFunc
	if deadline > 0 {
		childCtx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	proc, e := p.Backend.Start(childCtx, args, extra, stdin, stdout, stderr)
	if e != nil {
		a.Phase = "client_reaped"
		a.Outcome = "start_failed"
		now := time.Now().UTC()
		a.CompletedAt = &now
		if saveErr := p.Store.SaveAttempt(a); saveErr != nil {
			return errors.Join(e, saveErr)
		}
		return e
	}
	now := time.Now().UTC()
	pid := proc.PID()
	a.Phase = "started"
	a.StartedAt = &now
	a.DiagnosticPID = &pid
	a.DiagnosticStartIdentity = now.Format(time.RFC3339Nano)
	if e = p.Store.SaveAttempt(a); e != nil {
		cancelled, stop := context.WithCancel(ctx)
		stop()
		waitErr := proc.Wait(cancelled)
		if errorCode(waitErr) != "E_UNCLEAN_ATTEMPT" {
			completed := time.Now().UTC()
			a.Phase = "client_reaped"
			a.Outcome = "cancelled"
			a.CompletedAt = &completed
		}
		if errors.Is(waitErr, context.Canceled) && errorCode(waitErr) != "E_STORAGE" {
			waitErr = nil
		}
		return errors.Join(e, waitErr, p.Store.SaveAttempt(a))
	}
	e = proc.Wait(childCtx)
	if errorCode(e) == "E_UNCLEAN_ATTEMPT" {
		return e
	}
	now = time.Now().UTC()
	a.CompletedAt = &now
	a.Phase = "client_reaped"
	if childCtx.Err() != nil {
		a.Outcome = "cancelled"
	} else {
		a.Outcome = "normal_exit"
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
	if childCtx.Err() != nil {
		return childCtx.Err()
	}
	return e
}
func (p Provider) controlAttempt(ctx context.Context, r *state.Record, kind string, args, extra []string, timeout time.Duration) ([]byte, error) {
	stdout := &limitOutput{max: 16 << 20}
	stderr := &tailOutput{}
	e := p.attempt(ctx, r, kind, args, extra, nil, stdout, stderr, timeout)
	if stdout.overflow {
		return nil, compat.Fail("E_SBX_SCHEMA", "sbx control output exceeded 16 MiB")
	}
	if e != nil {
		return nil, p.quotedError(ctx, kind, e)
	}
	return stdout.data, nil
}
