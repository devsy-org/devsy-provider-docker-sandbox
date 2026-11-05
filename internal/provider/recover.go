package provider

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
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
		return compat.Fail(
			"E_ACTIVE_SESSIONS",
			"cannot acknowledge attempt while sessions are active",
		)
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.recoverAttemptLocked(id)
}

func (p Provider) recoverAttemptLocked(id string) error {
	r, e := p.load()
	if e != nil {
		return e
	}
	a, e := p.Store.ReadAttempt(id)
	if e != nil {
		return e
	}
	if a.Generation != r.Generation {
		return compat.Fail(codeUncleanAttempt, "attempt belongs to another generation")
	}
	if a.Phase == phaseClientReaped || a.Phase == phaseReconciled {
		return nil
	}
	if e := p.markAttemptOperationUncertain(&r, a); e != nil {
		return e
	}
	now := time.Now().UTC()
	a.CompletedAt = &now
	a.Phase = phaseReconciled
	a.Outcome = outcomeUnknown
	return p.Store.SaveAttempt(a)
}

func (p Provider) markAttemptOperationUncertain(r *state.Record, a state.Attempt) error {
	if r.Operation != nil && a.OperationID == r.Operation.ID && a.Kind != kindProbe {
		r.Operation.Phase = phaseUncertain
		return p.Store.Save(*r)
	}
	return nil
}

func (p Provider) requireCreateAttempt(r state.Record) error {
	if r.Operation == nil || r.Operation.Kind != kindCreate || r.Operation.LastAttemptID == "" {
		return compat.Fail(
			codeOperationUncertain,
			"creation provenance is missing for owned sandbox %s",
			r.SandboxName,
		)
	}
	attempt, err := p.Store.ReadAttempt(r.Operation.LastAttemptID)
	if err != nil {
		return err
	}
	if !matchesCreateAttempt(attempt, r) {
		return compat.Fail(
			codeOperationUncertain,
			"exact started creation attempt %s is not durably established",
			r.Operation.LastAttemptID,
		)
	}
	return nil
}

func matchesCreateAttempt(attempt state.Attempt, r state.Record) bool {
	if !matchesCreateIdentity(attempt, r) {
		return false
	}
	if !matchesCreateTiming(attempt) {
		return false
	}
	return matchesCreateOutcome(attempt)
}

func matchesCreateOutcome(attempt state.Attempt) bool {
	if attempt.Phase == phaseClientReaped {
		return attempt.Outcome == outcomeNormalExit || attempt.Outcome == outcomeCancelled
	}
	if attempt.Phase == phaseReconciled {
		return attempt.Outcome == outcomeUnknown
	}
	return false
}

func matchesCreateIdentity(attempt state.Attempt, r state.Record) bool {
	return attempt.OperationID == r.Operation.ID && attempt.MachineKey == r.MachineKey &&
		attempt.Generation == r.Generation && attempt.SandboxName == r.SandboxName &&
		attempt.Epoch == r.Epoch && attempt.Kind == kindCreate
}

func matchesCreateTiming(attempt state.Attempt) bool {
	return attempt.StartedAt != nil && !attempt.StartedAt.IsZero() &&
		attempt.CompletedAt != nil && !attempt.CompletedAt.IsZero() &&
		attempt.DiagnosticPID != nil && *attempt.DiagnosticPID > 0 &&
		attempt.DiagnosticStartIdentity != ""
}

func (p Provider) RecoverOperation(ctx context.Context, id string) (result error) {
	if _, e := p.live(ctx, false); e != nil {
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
	return p.recoverOperationLocked(ctx, id)
}

func (p Provider) recoverOperationLocked(ctx context.Context, id string) error {
	r, e := p.load()
	if e != nil {
		return e
	}
	if r.Operation == nil || r.Operation.ID != id || r.Operation.Phase != phaseUncertain {
		return compat.Fail(codeOperationUncertain, "exact uncertain operation not found")
	}
	attempts, e := p.Store.Incomplete()
	if e != nil {
		return e
	}
	if len(attempts) > 0 {
		return compat.Fail(
			codeUncleanAttempt,
			"reconcile child attempts before backend acknowledgement",
		)
	}
	item, found, e := p.observeAllowingAttached(ctx, r)
	if e != nil {
		return e
	}
	return p.settleObservedOperation(&r, item, found)
}

func (p Provider) settleObservedOperation(r *state.Record, item sbx.Sandbox, found bool) error {
	if found && p.Profile.InventoryStates[item.State] == stateTransitional {
		return compat.Fail(codeOperationUncertain, "backend remains transitional")
	}
	if e := p.acceptSettledCreateIdentity(r, item, found); e != nil {
		return e
	}
	kind := r.Operation.Kind
	if kind == kindDelete && !found {
		return p.successful(r, phaseDeleted, phaseDeleted)
	}
	if !found && r.SandboxID != "" {
		e := compat.Fail("E_MACHINE_MISSING", "previously recorded backend ID unavailable")
		r.Operation = nil
		return p.markFailed(r, e)
	}
	return p.markSettledOperationUncertain(r)
}

func (p Provider) acceptSettledCreateIdentity(r *state.Record, item sbx.Sandbox, found bool) error {
	if found && r.SandboxID == "" {
		if err := p.requireCreateAttempt(*r); err != nil {
			return err
		}
		r.SandboxID = item.ID
	}
	return nil
}

func (p Provider) markSettledOperationUncertain(r *state.Record) error {
	r.Operation = nil
	r.Phase = phaseFailed
	r.LastErrorCode = codeOperationUncertain
	if e := p.Store.Save(*r); e != nil {
		return e
	}
	return compat.Fail(
		codeOperationUncertain,
		"backend operation acknowledged but original postcondition must be explicitly reconciled",
	)
}

func (p Provider) MarkUncertain(r *state.Record, e error) error {
	if r.Operation != nil {
		r.Operation.Phase = phaseUncertain
		return errors.Join(e, p.Store.Save(*r))
	}
	return e
}
