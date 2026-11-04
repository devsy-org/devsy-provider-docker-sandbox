package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"io"
	"time"
)

func (p Provider) Command(ctx context.Context, program string, stdin io.Reader, stdout, stderr io.Writer) (result error) {
	if e := p.live(ctx, true); e != nil {
		return e
	}
	admissionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var r state.Record
	var session *state.Lock
	for {
		observed, observeErr := p.load()
		if observeErr != nil {
			return observeErr
		}
		if _, observeErr = p.ensureIdentity(admissionCtx, observed); observeErr != nil {
			return observeErr
		}
		lock, e := p.Store.Acquire(admissionCtx, "admission", false)
		if e != nil {
			return e
		}
		r, e = p.load()
		if e == nil {
			e = p.checkSpec(r)
		}
		if e == nil && (r.Phase != "ready" || r.DesiredState != "running" || r.Operation != nil) {
			e = compat.Fail("E_ACTIVE_SESSIONS", "machine is not accepting commands")
		}
		if e == nil && (r.Generation != observed.Generation || r.Epoch != observed.Epoch || r.SandboxID != observed.SandboxID) {
			e = compat.Fail("E_ACTIVE_SESSIONS", "machine changed during admission")
		}
		if e == nil {
			exclusive, exclusiveErr := p.Store.TryAcquire("sessions", false)
			if exclusiveErr == nil {
				e = errors.Join(p.unsettled(r), closeLock(exclusive))
			} else if !errors.Is(exclusiveErr, context.Canceled) {
				e = compat.Wrap("E_STORAGE", exclusiveErr)
			}
		}
		if e == nil {
			lease, leaseErr := p.Store.TryAcquire("sessions", true)
			if leaseErr == nil {
				session = lease
			} else if !errors.Is(leaseErr, context.Canceled) {
				e = compat.Wrap("E_STORAGE", leaseErr)
			} else if admissionCtx.Err() == nil {
				if closeErr := closeLock(lock); closeErr != nil {
					return closeErr
				}
				select {
				case <-admissionCtx.Done():
					return admissionCtx.Err()
				case <-time.After(25 * time.Millisecond):
				}
				continue
			} else {
				e = admissionCtx.Err()
			}
		}
		if e != nil {
			return errors.Join(e, closeLock(lock))
		}
		attemptID, e := state.Token()
		if e != nil {
			return errors.Join(e, closeLock(session), closeLock(lock))
		}
		a := state.Attempt{SchemaVersion: 1, AttemptID: attemptID, MachineKey: r.MachineKey, Generation: r.Generation, SandboxName: r.SandboxName, Epoch: r.Epoch, Kind: "command", Phase: "admitted", Outcome: "unknown"}
		e = p.Store.SaveAttempt(a)
		if e != nil {
			return errors.Join(e, closeLock(session), closeLock(lock))
		}
		proc, e := p.Backend.Start(ctx, sbx.SessionExecArgs(r.SandboxName, program, attemptID), nil, stdin, stdout, stderr)
		if e != nil {
			a.Phase = "client_reaped"
			a.Outcome = "start_failed"
			now := time.Now().UTC()
			a.CompletedAt = &now
			saveErr := p.Store.SaveAttempt(a)
			return errors.Join(e, saveErr, closeLock(session), closeLock(lock))
		}
		now := time.Now().UTC()
		pid := proc.PID()
		a.Phase = "started"
		a.StartedAt = &now
		a.DiagnosticPID = &pid
		saveErr := p.Store.SaveAttempt(a)
		closeErr := closeLock(lock)
		if saveErr != nil || closeErr != nil {
			forced, stop := context.WithCancel(ctx)
			stop()
			waitErr := proc.Wait(forced)
			if errorCode(waitErr) != "E_UNCLEAN_ATTEMPT" {
				completed := time.Now().UTC()
				a.Phase = "client_reaped"
				a.Outcome = "cancelled"
				a.CompletedAt = &completed
				saveErr = errors.Join(saveErr, p.Store.SaveAttempt(a))
			}
			if !errors.Is(waitErr, context.Canceled) || errorCode(waitErr) == "E_STORAGE" {
				saveErr = errors.Join(saveErr, waitErr)
			}
			return errors.Join(saveErr, closeErr, closeLock(session))
		}
		defer func() { result = errors.Join(result, closeLock(session)) }()
		watchCtx, stopWatch := context.WithCancel(ctx)
		done := make(chan struct{})
		watchErr := make(chan error, 1)
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			defer close(done)
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					current, readErr := p.Store.Load()
					if readErr != nil || current.Epoch != r.Epoch || current.Generation != r.Generation || current.Phase != "ready" || current.DesiredState != "running" || current.Operation != nil || current.SandboxID != r.SandboxID {
						watchErr <- compat.Fail("E_ACTIVE_SESSIONS", "machine epoch or intent changed")
						stopWatch()
						return
					}
				}
			}
		}()
		e = proc.Wait(watchCtx)
		cancelled := watchCtx.Err() != nil
		stopWatch()
		<-done
		if errorCode(e) == "E_UNCLEAN_ATTEMPT" {
			return e
		}
		var interrupted error
		select {
		case interrupted = <-watchErr:
		default:
		}
		completed := time.Now().UTC()
		a.Phase = "client_reaped"
		a.CompletedAt = &completed
		a.Outcome = "normal_exit"
		if interrupted != nil {
			a.Outcome = "interrupted"
		} else if cancelled {
			a.Outcome = "cancelled"
		}
		if code, ok := sbx.ExitCode(e); ok {
			a.ExitCode = &code
		} else if e == nil {
			zero := 0
			a.ExitCode = &zero
		}
		if saveErr = p.Store.SaveAttempt(a); saveErr != nil {
			return errors.Join(e, saveErr)
		}
		var terminateErr error
		if interrupted == nil && cancelled {
			terminateErr = p.terminateGuestSession(r.SandboxName, attemptID)
		}
		if interrupted != nil {
			return interrupted
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), terminateErr)
		}
		return errors.Join(e, terminateErr)
	}
}

func (p Provider) terminateGuestSession(name, session string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	proc, e := p.Backend.Start(ctx, sbx.TerminateSessionArgs(name, session), nil, nil, io.Discard, io.Discard)
	if e != nil {
		return compat.Fail("E_SESSION_UNTERMINATED", "guest session %s may still be running: %v", session, e)
	}
	if e = proc.Wait(ctx); e != nil {
		return compat.Fail("E_SESSION_UNTERMINATED", "guest session %s may still be running: %v", session, e)
	}
	return nil
}
