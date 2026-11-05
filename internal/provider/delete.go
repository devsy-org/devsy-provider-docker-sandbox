package provider

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func (p Provider) Delete(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if e := p.Profile.RequireOperationResultSchema(); e != nil {
		return e
	}
	bound, e := p.live(ctx, false)
	if e != nil {
		return e
	}
	return bound.deleteAdmitted(ctx)
}

func (p Provider) deleteAdmitted(ctx context.Context) (result error) {
	mutation, e := p.mutations(ctx)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(mutation)) }()
	r, e := p.load()
	if e != nil {
		return e
	}
	if e = p.guardPrior(r); e != nil {
		return e
	}
	sessions, e := p.begin(ctx, &r, transition{"deleting", phaseDeleted, kindDelete, "rm"})
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.deleteLocked(ctx, r)
}

func (p Provider) deleteLocked(ctx context.Context, r state.Record) error {
	_, found, e := p.observeAllowingAttached(ctx, r)
	if e != nil {
		return p.failWithStep(&r, e)
	}
	if !found {
		return p.successful(&r, phaseDeleted, phaseDeleted)
	}
	if r.SandboxID == "" {
		return p.failWithStep(
			&r,
			compat.Fail(
				codeOwnershipMismatch,
				"cannot delete a sandbox with no durably observed ID",
			),
		)
	}
	out, e := p.controlAttempt(
		ctx,
		&r,
		controlRequest{kind: "rm", args: sbx.RemoveArgs(r.SandboxName), timeout: 3 * time.Minute},
	)
	if e != nil {
		return p.MarkUncertain(
			&r,
			errors.Join(
				compat.Fail(
					codeOperationUncertain,
					"remove operation %s outcome uncertain",
					r.Operation.ID,
				),
				e,
			),
		)
	}
	if e = sbx.CheckResult(out, r.SandboxName, "removed", p.lenient); e != nil {
		return p.MarkUncertain(&r, e)
	}
	e = waitState(
		ctx,
		200*time.Millisecond,
		func() (bool, error) { _, found, e := p.observeAllowingAttached(ctx, r); return !found, e },
	)
	if e != nil {
		return p.MarkUncertain(&r, e)
	}
	return p.successful(&r, phaseDeleted, phaseDeleted)
}
