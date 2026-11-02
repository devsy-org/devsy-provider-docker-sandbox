package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"time"
)

func (p Provider) Delete(ctx context.Context) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
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
	sessions, e := p.begin(ctx, &r, "deleting", "deleted", "delete", "rm")
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	_, found, e := p.observe(ctx, r)
	if e != nil {
		return p.failWithStep(&r, e)
	}
	if !found {
		return p.successful(&r, "deleted", "deleted")
	}
	if r.SandboxID == "" {
		return compat.Fail("E_OWNERSHIP_MISMATCH", "cannot delete a sandbox with no durably observed ID")
	}
	out, e := p.controlAttempt(ctx, &r, "rm", sbx.RemoveArgs(r.SandboxName), nil, 3*time.Minute)
	if e != nil {
		return p.MarkUncertain(&r, errors.Join(compat.Fail("E_OPERATION_UNCERTAIN", "remove operation %s outcome uncertain", r.Operation.ID), e))
	}
	if e = sbx.CheckResult(out, r.SandboxName); e != nil {
		return p.MarkUncertain(&r, e)
	}
	e = waitState(ctx, 200*time.Millisecond, func() (bool, error) { _, found, e := p.observe(ctx, r); return !found, e })
	if e != nil {
		return p.MarkUncertain(&r, e)
	}
	return p.successful(&r, "deleted", "deleted")
}
