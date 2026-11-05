package provider

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

func (p Provider) Stop(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if e := p.Profile.RequireOperationResultSchema(); e != nil {
		return e
	}
	bound, e := p.live(ctx, false)
	if e != nil {
		return e
	}
	return bound.stopAdmitted(ctx)
}

func (p Provider) stopAdmitted(ctx context.Context) (result error) {
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
	sessions, e := p.begin(ctx, &r, transition{"stopping", stateStopped, kindStop, kindStop})
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	return p.stopLocked(ctx, r)
}

func (p Provider) stopLocked(ctx context.Context, r state.Record) error {
	item, e := p.ensureIdentity(ctx, r)
	if e != nil {
		return p.failWithStep(&r, e)
	}
	if p.Profile.InventoryStates[item.State] == stateStopped {
		return p.successful(&r, stateStopped, stateStopped)
	}
	out, e := p.controlAttempt(
		ctx,
		&r,
		controlRequest{kind: kindStop, args: sbx.StopArgs(r.SandboxName), timeout: 2 * time.Minute},
	)
	if e != nil {
		return p.MarkUncertain(
			&r,
			errors.Join(
				compat.Fail(
					codeOperationUncertain,
					"stop operation %s outcome uncertain",
					r.Operation.ID,
				),
				e,
			),
		)
	}
	if e = sbx.CheckResult(out, r.SandboxName, stateStopped, p.lenient); e != nil {
		return p.MarkUncertain(&r, e)
	}
	e = waitState(ctx, 200*time.Millisecond, func() (bool, error) {
		item, e := p.ensureIdentity(ctx, r)
		if e != nil {
			return false, e
		}
		return p.Profile.InventoryStates[item.State] == stateStopped, nil
	})
	if e != nil {
		return p.MarkUncertain(&r, e)
	}
	return p.successful(&r, stateStopped, stateStopped)
}
