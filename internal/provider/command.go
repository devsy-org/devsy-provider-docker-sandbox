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

type CommandStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (p Provider) Command(
	ctx context.Context,
	program string,
	streams CommandStreams,
) (result error) {
	quiet := p
	quiet.Warner = nil
	if _, e := quiet.live(ctx, true); e != nil {
		return e
	}
	admissionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	admission, e := p.admitCommand(admissionCtx)
	if e != nil {
		return e
	}
	proc, attempt, e := p.launchCommand(ctx, admission, program, streams)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(admission.session)) }()
	return p.waitCommand(ctx, admission.record, attempt, proc)
}

type commandAdmission struct {
	record  state.Record
	session *state.Lock
	lock    *state.Lock
}

func (p Provider) admitCommand(ctx context.Context) (commandAdmission, error) {
	for {
		observed, e := p.observeCommandAdmission(ctx)
		if e != nil {
			return commandAdmission{}, e
		}
		lock, e := p.Store.Acquire(ctx, "admission", false)
		if e != nil {
			return commandAdmission{}, e
		}
		r, e := p.validateCommandAdmission(observed)
		if e != nil {
			return commandAdmission{}, errors.Join(e, closeLock(lock))
		}
		session, retry, e := p.commandSession(ctx, lock)
		if retry {
			continue
		}
		if e != nil {
			return commandAdmission{}, e
		}
		return commandAdmission{record: r, session: session, lock: lock}, nil
	}
}

func (p Provider) observeCommandAdmission(ctx context.Context) (state.Record, error) {
	observed, e := p.load()
	if e != nil {
		return observed, e
	}
	_, e = p.ensureIdentity(ctx, observed)
	return observed, e
}

func (p Provider) validateCommandAdmission(observed state.Record) (state.Record, error) {
	r, e := p.load()
	if e == nil {
		e = p.checkCommandAdmission(r, observed)
	}
	if e == nil {
		e = p.checkCommandUnsettled(r)
	}
	return r, e
}

func (p Provider) checkCommandAdmission(r, observed state.Record) error {
	if e := p.checkSpec(r); e != nil {
		return e
	}
	if r.Phase != phaseReady || r.DesiredState != stateRunning || r.Operation != nil {
		return compat.Fail("E_ACTIVE_SESSIONS", "machine is not accepting commands")
	}
	if r.Generation != observed.Generation || r.Epoch != observed.Epoch ||
		r.SandboxID != observed.SandboxID {
		return compat.Fail("E_ACTIVE_SESSIONS", "machine changed during admission")
	}
	return nil
}

func (p Provider) checkCommandUnsettled(r state.Record) error {
	exclusive, e := p.Store.TryAcquire("sessions", false)
	if e == nil {
		return errors.Join(p.unsettled(r), closeLock(exclusive))
	}
	if !errors.Is(e, context.Canceled) {
		return compat.Wrap("E_STORAGE", e)
	}
	return nil
}

func (p Provider) commandSession(ctx context.Context, lock *state.Lock) (*state.Lock, bool, error) {
	lease, e := p.Store.TryAcquire("sessions", true)
	switch {
	case e == nil:
		return lease, false, nil
	case !errors.Is(e, context.Canceled):
		return nil, false, errors.Join(compat.Wrap("E_STORAGE", e), closeLock(lock))
	case ctx.Err() != nil:
		return nil, false, errors.Join(ctx.Err(), closeLock(lock))
	default:
		if closeErr := closeLock(lock); closeErr != nil {
			return nil, false, closeErr
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
			return nil, true, nil
		}
	}
}

func (p Provider) launchCommand(
	ctx context.Context,
	admission commandAdmission,
	program string,
	streams CommandStreams,
) (*sbx.Process, state.Attempt, error) {
	r, session, lock := admission.record, admission.session, admission.lock
	attemptID, e := state.Token()
	if e != nil {
		return nil, state.Attempt{}, errors.Join(e, closeLock(session), closeLock(lock))
	}
	a := state.Attempt{
		SchemaVersion: 1, AttemptID: attemptID, MachineKey: r.MachineKey,
		Generation: r.Generation, SandboxName: r.SandboxName, Epoch: r.Epoch,
		Kind: kindCommand, Phase: "admitted", Outcome: outcomeUnknown,
	}
	if e = p.Store.SaveAttempt(a); e != nil {
		return nil, a, errors.Join(e, closeLock(session), closeLock(lock))
	}
	proc, e := p.Backend.Start(ctx, sbx.StartRequest{
		Args:  sbx.SessionExecArgs(r.SandboxName, program, attemptID),
		Stdin: streams.Stdin, Stdout: streams.Stdout, Stderr: streams.Stderr,
	})
	if e != nil {
		a.Phase = phaseClientReaped
		a.Outcome = "start_failed"
		now := time.Now().UTC()
		a.CompletedAt = &now
		saveErr := p.Store.SaveAttempt(a)
		return nil, a, errors.Join(e, saveErr, closeLock(session), closeLock(lock))
	}
	now := time.Now().UTC()
	pid := proc.PID()
	a.Phase = phaseStarted
	a.StartedAt = &now
	a.DiagnosticPID = &pid
	saveErr := p.Store.SaveAttempt(a)
	closeErr := closeLock(lock)
	if saveErr != nil || closeErr != nil {
		return nil, a, p.abortStartedCommand(
			ctx,
			commandAbort{
				proc:     proc,
				attempt:  &a,
				session:  session,
				saveErr:  saveErr,
				closeErr: closeErr,
			},
		)
	}
	return proc, a, nil
}

type commandAbort struct {
	proc              *sbx.Process
	attempt           *state.Attempt
	session           *state.Lock
	saveErr, closeErr error
}

func (p Provider) abortStartedCommand(ctx context.Context, abort commandAbort) error {
	proc, a, session := abort.proc, abort.attempt, abort.session
	saveErr, closeErr := abort.saveErr, abort.closeErr
	forced, stop := context.WithCancel(ctx)
	stop()
	waitErr := proc.Wait(forced)
	if errorCode(waitErr) != codeUncleanAttempt {
		completed := time.Now().UTC()
		a.Phase = phaseClientReaped
		a.Outcome = outcomeCancelled
		a.CompletedAt = &completed
		saveErr = errors.Join(saveErr, p.Store.SaveAttempt(*a))
	}
	if !errors.Is(waitErr, context.Canceled) || errorCode(waitErr) == "E_STORAGE" {
		saveErr = errors.Join(saveErr, waitErr)
	}
	return errors.Join(saveErr, closeErr, closeLock(session))
}

func (p Provider) waitCommand(
	ctx context.Context,
	r state.Record,
	a state.Attempt,
	proc *sbx.Process,
) error {
	watchCtx, stopWatch := context.WithCancel(ctx)
	done := make(chan struct{})
	watchErr := make(chan error, 1)
	go p.watchCommand(
		commandWatch{ctx: watchCtx, stop: stopWatch, record: r, done: done, watchErr: watchErr},
	)
	e := proc.Wait(watchCtx)
	cancelled := watchCtx.Err() != nil
	stopWatch()
	<-done
	if errorCode(e) == codeUncleanAttempt {
		return e
	}
	var interrupted error
	select {
	case interrupted = <-watchErr:
	default:
	}
	a = completeCommandAttempt(a, e, interrupted, cancelled)
	if saveErr := p.Store.SaveAttempt(a); saveErr != nil {
		return errors.Join(e, saveErr)
	}
	return p.commandWaitResult(
		ctx,
		commandResult{
			name:        r.SandboxName,
			attemptID:   a.AttemptID,
			waitErr:     e,
			interrupted: interrupted,
			cancelled:   cancelled,
		},
	)
}

type commandResult struct {
	name, attemptID      string
	waitErr, interrupted error
	cancelled            bool
}

func (p Provider) commandWaitResult(ctx context.Context, result commandResult) error {
	name, attemptID, waitErr := result.name, result.attemptID, result.waitErr
	interrupted, cancelled := result.interrupted, result.cancelled
	var terminateErr error
	if interrupted == nil && cancelled {
		terminateErr = p.terminateGuestSession(name, attemptID)
	}
	if interrupted != nil {
		return interrupted
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), terminateErr)
	}
	return errors.Join(waitErr, terminateErr)
}

func completeCommandAttempt(a state.Attempt, e, interrupted error, cancelled bool) state.Attempt {
	completed := time.Now().UTC()
	a.Phase = phaseClientReaped
	a.CompletedAt = &completed
	a.Outcome = outcomeNormalExit
	if interrupted != nil {
		a.Outcome = "interrupted"
	} else if cancelled {
		a.Outcome = outcomeCancelled
	}
	if code, ok := sbx.ExitCode(e); ok {
		a.ExitCode = &code
	} else if e == nil {
		zero := 0
		a.ExitCode = &zero
	}
	return a
}

type commandWatch struct {
	ctx      context.Context
	stop     context.CancelFunc
	record   state.Record
	done     chan struct{}
	watchErr chan error
}

func (p Provider) watchCommand(watch commandWatch) {
	ctx, stop, r, done, watchErr := watch.ctx, watch.stop, watch.record, watch.done, watch.watchErr
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, e := p.Store.Load()
			if e != nil || !sameCommandEpoch(current, r) {
				watchErr <- compat.Fail("E_ACTIVE_SESSIONS", "machine epoch or intent changed")
				stop()
				return
			}
		}
	}
}

func sameCommandEpoch(current, r state.Record) bool {
	return current.Epoch == r.Epoch && current.Generation == r.Generation &&
		current.Phase == phaseReady && current.DesiredState == stateRunning &&
		current.Operation == nil && current.SandboxID == r.SandboxID
}

func (p Provider) terminateGuestSession(name, session string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	proc, e := p.Backend.Start(ctx, sbx.StartRequest{
		Args: sbx.TerminateSessionArgs(name, session), Stdout: io.Discard, Stderr: io.Discard,
	})
	if e != nil {
		return compat.Fail(
			"E_SESSION_UNTERMINATED",
			"guest session %s may still be running: %v",
			session,
			e,
		)
	}
	if e = proc.Wait(ctx); e != nil {
		return compat.Fail(
			"E_SESSION_UNTERMINATED",
			"guest session %s may still be running: %v",
			session,
			e,
		)
	}
	return nil
}
