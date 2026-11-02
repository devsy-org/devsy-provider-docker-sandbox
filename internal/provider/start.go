package provider

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
	"time"
)

func (p Provider) Start(ctx context.Context) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if e := p.live(ctx, true); e != nil {
		return e
	}
	if e := p.Profile.RequireDiskSizingQualification(); e != nil {
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
	if e = p.checkSpec(r); e != nil {
		return e
	}
	return p.startLocked(ctx, &r)
}
func (p Provider) startLocked(ctx context.Context, r *state.Record) (result error) {
	if r.SandboxID == "" {
		return compat.Fail("E_STATE_MISSING", "machine backend ID has never been recorded")
	}
	item, e := p.ensureIdentity(ctx, *r)
	if e != nil {
		return e
	}
	if r.Phase == "ready" && r.DesiredState == "running" && r.Operation == nil && p.Profile.InventoryStates[item.State] == "running" {
		sessions, e := p.Store.Acquire(ctx, "sessions", true)
		if e != nil {
			return e
		}
		defer func() { result = errors.Join(result, closeLock(sessions)) }()
		return p.readiness(ctx, r)
	}
	sessions, e := p.begin(ctx, r, "starting", "running", "start", "run")
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, closeLock(sessions)) }()
	item, e = p.ensureIdentity(ctx, *r)
	if e != nil {
		return p.failWithStep(r, e)
	}
	if p.Profile.InventoryStates[item.State] == "running" {
		if e = p.readiness(ctx, r); e != nil {
			return p.failWithStep(r, e)
		}
		return p.successful(r, "ready", "running")
	}
	return p.runReady(ctx, r)
}
