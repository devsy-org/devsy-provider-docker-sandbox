package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"time"
)

func (p Provider) Stop(ctx context.Context) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if e := p.Profile.RequireOperationResultSchema(); e != nil {
		return e
	}
	if e := p.live(ctx, false); e != nil {
		return e
	}
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
	sessions, e := p.begin(ctx, &r, "stopping", "stopped", "stop", "stop")
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	item, e := p.ensureIdentity(ctx, r)
	if e != nil {
		return p.failWithStep(&r, e)
	}
	if p.Profile.InventoryStates[item.State] == "stopped" {
		return p.successful(&r, "stopped", "stopped")
	}
	out, e := p.controlAttempt(ctx, &r, "stop", sbx.StopArgs(r.SandboxName), nil, 2*time.Minute)
	if e != nil {
		return p.MarkUncertain(&r, errors.Join(compat.Fail("E_OPERATION_UNCERTAIN", "stop operation %s outcome uncertain", r.Operation.ID), e))
	}
	if e = sbx.CheckResult(out, r.SandboxName, "stopped"); e != nil {
		return p.MarkUncertain(&r, e)
	}
	e = waitState(ctx, 200*time.Millisecond, func() (bool, error) {
		item, e := p.ensureIdentity(ctx, r)
		if e != nil {
			return false, e
		}
		return p.Profile.InventoryStates[item.State] == "stopped", nil
	})
	if e != nil {
		return p.MarkUncertain(&r, e)
	}
	return p.successful(&r, "stopped", "stopped")
}
