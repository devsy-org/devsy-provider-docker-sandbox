package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"time"
)

func (p Provider) RecoverAttempt(ctx context.Context, id string) (result error) {
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	admission, e := p.Store.Acquire(ctx, "admission", false)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(admission)) }()
	sessions, e := p.Store.TryAcquire("sessions", false)
	if e != nil {
		return compat.Fail("E_ACTIVE_SESSIONS", "cannot acknowledge attempt while sessions are active")
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	r, e := p.load()
	if e != nil {
		return e
	}
	a, e := p.Store.ReadAttempt(id)
	if e != nil {
		return e
	}
	if a.Generation != r.Generation {
		return compat.Fail("E_UNCLEAN_ATTEMPT", "attempt belongs to another generation")
	}
	if a.Phase == "client_reaped" || a.Phase == "reconciled" {
		return nil
	}
	if r.Operation != nil && a.OperationID == r.Operation.ID && a.Kind != "probe" {
		r.Operation.Phase = "uncertain"
		if e = p.Store.Save(r); e != nil {
			return e
		}
	}
	now := time.Now().UTC()
	a.CompletedAt = &now
	a.Phase = "reconciled"
	a.Outcome = "unknown"
	return p.Store.SaveAttempt(a)
}
func (p Provider) requireCreateAttempt(r state.Record) error {
	if r.Operation == nil || r.Operation.Kind != "create" || r.Operation.LastAttemptID == "" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "creation provenance is missing for owned sandbox %s", r.SandboxName)
	}
	attempt, err := p.Store.ReadAttempt(r.Operation.LastAttemptID)
	if err != nil {
		return err
	}
	if attempt.OperationID != r.Operation.ID ||
		attempt.MachineKey != r.MachineKey ||
		attempt.Generation != r.Generation ||
		attempt.SandboxName != r.SandboxName ||
		attempt.Epoch != r.Epoch ||
		attempt.Kind != "create" ||
		attempt.StartedAt == nil ||
		attempt.StartedAt.IsZero() ||
		attempt.CompletedAt == nil ||
		attempt.CompletedAt.IsZero() ||
		attempt.DiagnosticPID == nil ||
		*attempt.DiagnosticPID <= 0 ||
		attempt.DiagnosticStartIdentity == "" ||
		(attempt.Phase == "client_reaped" && attempt.Outcome != "normal_exit" && attempt.Outcome != "cancelled") ||
		(attempt.Phase == "reconciled" && attempt.Outcome != "unknown") ||
		(attempt.Phase != "client_reaped" && attempt.Phase != "reconciled") {
		return compat.Fail("E_OPERATION_UNCERTAIN", "exact started creation attempt %s is not durably established", r.Operation.LastAttemptID)
	}
	return nil
}

func (p Provider) RecoverOperation(ctx context.Context, id string) (result error) {
	if e := p.live(ctx, false); e != nil {
		return e
	}
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	admission, e := p.Store.Acquire(ctx, "admission", false)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(admission)) }()
	sessions, e := p.Store.TryAcquire("sessions", false)
	if e != nil {
		return compat.Fail("E_ACTIVE_SESSIONS", "cannot settle operation while sessions are active")
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	r, e := p.load()
	if e != nil {
		return e
	}
	if r.Operation == nil || r.Operation.ID != id || r.Operation.Phase != "uncertain" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "exact uncertain operation not found")
	}
	attempts, e := p.Store.Incomplete()
	if e != nil {
		return e
	}
	if len(attempts) > 0 {
		return compat.Fail("E_UNCLEAN_ATTEMPT", "reconcile child attempts before backend acknowledgement")
	}
	item, found, e := p.observe(ctx, r)
	if e != nil {
		return e
	}
	if found && p.Profile.InventoryStates[item.State] == "transitional" {
		return compat.Fail("E_OPERATION_UNCERTAIN", "backend remains transitional")
	}
	if found && r.SandboxID == "" {
		if err := p.requireCreateAttempt(r); err != nil {
			return err
		}
		r.SandboxID = item.ID
	}
	kind := r.Operation.Kind
	if kind == "delete" && !found {
		return p.successful(&r, "deleted", "deleted")
	}
	if !found && r.SandboxID != "" {
		e = compat.Fail("E_MACHINE_MISSING", "previously recorded backend ID unavailable")
		r.Operation = nil
		return p.markFailed(&r, e)
	}
	r.Operation = nil
	r.Phase = "failed"
	r.LastErrorCode = "E_OPERATION_UNCERTAIN"
	if e = p.Store.Save(r); e != nil {
		return e
	}
	return compat.Fail("E_OPERATION_UNCERTAIN", "backend operation acknowledged but original postcondition must be explicitly reconciled")
}
func (p Provider) MarkUncertain(r *state.Record, e error) error {
	if r.Operation != nil {
		r.Operation.Phase = "uncertain"
		return errors.Join(e, p.Store.Save(*r))
	}
	return e
}
